package urls

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chechiachang/abot/internal/media"
)

var youtubeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)

func (r *Reader) readReddit(ctx context.Context, source *url.URL) (Result, error) {
	segments := pathSegments(source)
	postID := ""
	for i, segment := range segments {
		if segment == "comments" && i+1 < len(segments) {
			postID = segments[i+1]
			break
		}
	}
	if postID == "" {
		return r.readGeneric(ctx, source, "reddit")
	}
	apiURL := &url.URL{Scheme: "https", Host: "www.reddit.com", Path: "/comments/" + postID + ".json", RawQuery: "raw_json=1"}
	resp, finalURL, err := r.fetchFollowingRedirects(ctx, http.MethodGet, apiURL.String(), nil, nil)
	if err != nil {
		return Result{}, err
	}
	title, content, err := extractJSON(resp.body)
	if err != nil || content == "" {
		return Result{}, &Error{Category: "extraction_failed", Message: "Reddit returned no readable post or comment text."}
	}
	return Result{FinalURL: finalURL, Source: "reddit", Title: title, Content: truncate(content, r.config.MaxOutputBytes)}, nil
}

func (r *Reader) readGitHub(ctx context.Context, source *url.URL) (Result, error) {
	segments := pathSegments(source)
	if len(segments) >= 5 && segments[2] == "blob" {
		branch := segments[3]
		file := ""
		if len(segments) > 4 {
			file = strings.Join(segments[4:], "/")
		}
		raw := &url.URL{Scheme: "https", Host: "raw.githubusercontent.com", Path: "/" + segments[0] + "/" + segments[1] + "/" + branch + "/" + file}
		resp, finalURL, err := r.fetchFollowingRedirects(ctx, http.MethodGet, raw.String(), nil, nil)
		if err != nil {
			return Result{}, err
		}
		return r.extractResponse(ctx, source.String(), finalURL, "github", resp)
	}
	if len(segments) >= 2 {
		apiURL := &url.URL{Scheme: "https", Host: "api.github.com", Path: "/repos/" + segments[0] + "/" + segments[1] + "/readme"}
		headers := make(http.Header)
		headers.Set("Accept", "application/vnd.github+json")
		resp, finalURL, err := r.fetchFollowingRedirects(ctx, http.MethodGet, apiURL.String(), headers, nil)
		if err != nil {
			if len(segments) > 2 {
				return r.readGeneric(ctx, source, "github")
			}
			return Result{}, err
		}
		var readme struct {
			Name     string `json:"name"`
			Encoding string `json:"encoding"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal(resp.body, &readme); err != nil || readme.Encoding != "base64" {
			return Result{}, &Error{Category: "extraction_failed", Message: "GitHub README response could not be decoded."}
		}
		content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(readme.Content, "\n", ""))
		if err != nil || len(content) == 0 {
			return Result{}, &Error{Category: "extraction_failed", Message: "GitHub README contained no readable text."}
		}
		return Result{FinalURL: finalURL, Source: "github", Title: readme.Name, Content: truncate(string(content), r.config.MaxOutputBytes)}, nil
	}
	return r.readGeneric(ctx, source, "github")
}

func (r *Reader) readGoogleDoc(ctx context.Context, source *url.URL) (Result, error) {
	segments := pathSegments(source)
	if len(segments) < 3 || segments[0] != "document" || segments[1] != "d" || segments[2] == "" {
		return Result{}, &Error{Category: "unsupported_source", Message: "Only public Google Docs documents are supported."}
	}
	export := &url.URL{Scheme: "https", Host: "docs.google.com", Path: "/document/d/" + segments[2] + "/export", RawQuery: "format=txt"}
	resp, finalURL, err := r.fetchFollowingRedirects(ctx, http.MethodGet, export.String(), nil, nil)
	if err != nil {
		return Result{}, err
	}
	final, _ := url.Parse(finalURL)
	if final != nil && strings.HasSuffix(strings.ToLower(final.Hostname()), "google.com") && strings.HasPrefix(final.Hostname(), "accounts.") {
		return Result{}, &Error{Category: "unsupported_source", Message: "Google Docs document must be publicly accessible without sign-in."}
	}
	return r.extractResponse(ctx, source.String(), finalURL, "google_docs", resp)
}

func (r *Reader) readTruthSocial(ctx context.Context, source *url.URL) (Result, error) {
	segments := pathSegments(source)
	if len(segments) == 0 {
		return r.readGeneric(ctx, source, "truth_social")
	}
	statusID := segments[len(segments)-1]
	if statusID == "" {
		return Result{}, &Error{Category: "unsupported_source", Message: "Truth Social status URL is not recognized."}
	}
	apiURL := &url.URL{Scheme: "https", Host: "truthsocial.com", Path: "/api/v1/statuses/" + statusID}
	resp, finalURL, err := r.fetchFollowingRedirects(ctx, http.MethodGet, apiURL.String(), nil, nil)
	if err != nil {
		return Result{}, err
	}
	title, content, err := extractJSON(resp.body)
	if err != nil || content == "" {
		return Result{}, &Error{Category: "extraction_failed", Message: "Truth Social status contained no readable text."}
	}
	return Result{FinalURL: finalURL, Source: "truth_social", Title: title, Content: truncate(content, r.config.MaxOutputBytes)}, nil
}

func (r *Reader) readYouTube(ctx context.Context, source *url.URL) (Result, error) {
	videoID := ""
	if strings.EqualFold(source.Hostname(), "youtu.be") {
		segments := pathSegments(source)
		if len(segments) > 0 {
			videoID = segments[0]
		}
	} else {
		videoID = source.Query().Get("v")
		segments := pathSegments(source)
		if videoID == "" && len(segments) > 1 && (segments[0] == "embed" || segments[0] == "shorts") {
			videoID = segments[1]
		}
	}
	if !youtubeIDPattern.MatchString(videoID) {
		return Result{}, &Error{Category: "unsupported_source", Message: "YouTube video URL is not recognized."}
	}
	playerURL := "https://www.youtube.com/youtubei/v1/player?prettyPrint=false"
	requestBody, _ := json.Marshal(map[string]any{
		"context": map[string]any{"client": map[string]any{"clientName": "ANDROID", "clientVersion": "20.10.38", "hl": "en"}},
		"videoId": videoID,
	})
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	resp, _, err := r.fetchFollowingRedirects(ctx, http.MethodPost, playerURL, headers, requestBody)
	if err != nil {
		if sourceErrorCategory(err) == "http_status" || sourceErrorCategory(err) == "extraction_failed" {
			return r.readYouTubeAudio(ctx, videoID, "")
		}
		return Result{}, err
	}
	var player struct {
		VideoDetails struct {
			Title string `json:"title"`
		} `json:"videoDetails"`
		Captions struct {
			Tracklist struct {
				Tracks []struct {
					BaseURL      string `json:"baseUrl"`
					LanguageCode string `json:"languageCode"`
				} `json:"captionTracks"`
			} `json:"playerCaptionsTracklistRenderer"`
		} `json:"captions"`
	}
	if err := json.Unmarshal(resp.body, &player); err != nil {
		return r.readYouTubeAudio(ctx, videoID, "")
	}
	tracks := player.Captions.Tracklist.Tracks
	if len(tracks) == 0 {
		return r.readYouTubeAudio(ctx, videoID, player.VideoDetails.Title)
	}
	track := tracks[0]
	for _, candidate := range tracks {
		if strings.HasPrefix(candidate.LanguageCode, "en") {
			track = candidate
			break
		}
	}
	captions, finalURL, err := r.fetchFollowingRedirects(ctx, http.MethodGet, track.BaseURL, nil, nil)
	if err != nil {
		return r.readYouTubeAudio(ctx, videoID, player.VideoDetails.Title)
	}
	text := parseCaptions(captions.body)
	if text == "" {
		return r.readYouTubeAudio(ctx, videoID, player.VideoDetails.Title)
	}
	return Result{FinalURL: finalURL, Source: "youtube_captions", Title: player.VideoDetails.Title, Content: truncate(text, r.config.MaxOutputBytes)}, nil
}

func sourceErrorCategory(err error) string {
	var sourceErr *Error
	if errors.As(err, &sourceErr) {
		return sourceErr.Category
	}
	return ""
}

func (r *Reader) readYouTubeAudio(ctx context.Context, videoID, title string) (Result, error) {
	if r.config.YouTubeDL == "" || r.media == nil {
		return Result{}, &Error{Category: "unavailable_captions", Message: "YouTube captions are unavailable and local audio transcription is not configured."}
	}
	tempDir, err := os.MkdirTemp("", "abot-youtube-audio-")
	if err != nil {
		return Result{}, &Error{Category: "extraction_failed", Message: "Could not create a private audio workspace."}
	}
	defer os.RemoveAll(tempDir)
	proxyURL, stopProxy, err := r.startHTTPSProxy(ctx)
	if err != nil {
		return Result{}, &Error{Category: "extraction_failed", Message: "Could not start a restricted YouTube download proxy."}
	}
	defer stopProxy()
	seconds := int(r.config.MaxAudioDuration / time.Second)
	if seconds < 1 {
		seconds = 300
	}
	videoURL := "https://www.youtube.com/watch?v=" + url.QueryEscape(videoID)
	output := filepath.Join(tempDir, "audio.%(ext)s")
	command := exec.CommandContext(ctx, r.config.YouTubeDL,
		"--no-config", "--no-playlist", "--quiet", "--no-warnings", "--no-progress", "--no-part",
		"--proxy", proxyURL, "--socket-timeout", "10", "--retries", "1",
		"--match-filter", "duration <= "+strconv.Itoa(seconds), "--download-sections", "*0-"+strconv.Itoa(seconds),
		"--max-filesize", strconv.FormatInt(r.config.MaxBytes, 10), "--extract-audio", "--audio-format", "opus",
		"--output", output, videoURL,
	)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tempDir, "HTTP_PROXY=" + proxyURL, "HTTPS_PROXY=" + proxyURL, "NO_PROXY="}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, contextError(ctx.Err())
		}
		return Result{}, &Error{Category: "extraction_failed", Message: "Bounded YouTube audio download failed."}
	}
	audioPath := filepath.Join(tempDir, "audio.opus")
	info, err := os.Stat(audioPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return Result{}, &Error{Category: "extraction_failed", Message: "YouTube downloader produced no audio file."}
	}
	if info.Size() > r.config.MaxBytes {
		return Result{}, &Error{Category: "limit", Message: "YouTube audio exceeds the configured download limit."}
	}
	transcript, err := r.media.TranscribeAudio(ctx, audioPath)
	if err != nil {
		var mediaErr *media.Error
		if errors.As(err, &mediaErr) {
			if mediaErr.Code == "transcription_unavailable" {
				return Result{}, &Error{Category: "unavailable_captions", Message: "YouTube captions are unavailable and local Whisper transcription is not configured."}
			}
			if mediaErr.Code == "too_long" || mediaErr.Code == "too_large" {
				return Result{}, &Error{Category: "limit", Message: mediaErr.Message}
			}
		}
		return Result{}, &Error{Category: "extraction_failed", Message: "Local YouTube audio transcription failed."}
	}
	if transcript == "" {
		return Result{}, &Error{Category: "unavailable_captions", Message: "YouTube captions and local transcription returned no text."}
	}
	return Result{Source: "youtube_audio_transcription", Title: title, Content: truncate(transcript, r.config.MaxOutputBytes)}, nil
}

func (r *Reader) readGeneric(ctx context.Context, source *url.URL, kind string) (Result, error) {
	resp, finalURL, err := r.fetchFollowingRedirects(ctx, http.MethodGet, source.String(), nil, nil)
	if err != nil {
		return Result{}, err
	}
	return r.extractResponse(ctx, source.String(), finalURL, kind, resp)
}

func pathSegments(value *url.URL) []string {
	var segments []string
	for _, raw := range strings.Split(strings.Trim(value.EscapedPath(), "/"), "/") {
		if raw == "" {
			continue
		}
		segment, err := url.PathUnescape(raw)
		if err != nil || segment == "." || segment == ".." || strings.Contains(segment, "/") || strings.Contains(segment, "\\") {
			return nil
		}
		segments = append(segments, segment)
	}
	return segments
}

func parseCaptions(data []byte) string {
	var transcript struct {
		Text []struct {
			Value string `xml:",chardata"`
		} `xml:"text"`
		Events []struct {
			Segments []struct {
				Value string `xml:",chardata"`
			} `xml:"s"`
		} `xml:"body>p"`
	}
	if xml.Unmarshal(data, &transcript) != nil {
		return ""
	}
	var fragments []string
	for _, item := range transcript.Text {
		fragments = append(fragments, item.Value)
	}
	for _, event := range transcript.Events {
		for _, segment := range event.Segments {
			fragments = append(fragments, segment.Value)
		}
	}
	return normalizeText(strings.Join(fragments, " "))
}
