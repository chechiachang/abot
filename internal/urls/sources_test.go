package urls

import (
	"context"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chechiachang/abot/internal/media"
)

func TestYouTubeCaptionAbsenceUsesBoundedLocalTranscriptionFallback(t *testing.T) {
	root := t.TempDir()
	ffprobe := writeURLCommand(t, root, "ffprobe", "printf '30\\n'")
	ffmpeg := writeURLCommand(t, root, "ffmpeg", `for arg do output=$arg; done; printf wav > "$output"`)
	whisper := writeURLCommand(t, root, "whisper", "printf 'local transcript'")
	downloader := writeURLCommand(t, root, "yt-dlp", `output=""; while [ "$#" -gt 0 ]; do if [ "$1" = "--output" ]; then shift; output=$1; fi; shift; done; file=${output%'.%(ext)s'}.opus; printf audio > "$file"`)
	store, err := media.NewStore(filepath.Join(root, "media"), media.Limits{MaxBytes: 1024, MaxDuration: time.Minute, MaxExtractBytes: 1024, FFprobe: ffprobe, FFmpeg: ffmpeg, Whisper: whisper, WhisperModel: "local-model"})
	if err != nil {
		t.Fatal(err)
	}
	reader := testReader(t, mapResolver{
		"youtu.be":        {netip.MustParseAddr("93.184.216.34")},
		"youtube.com":     {netip.MustParseAddr("93.184.216.34")},
		"www.youtube.com": {netip.MustParseAddr("93.184.216.34")},
	})
	reader.media = store
	reader.config.YouTubeDL = downloader
	reader.config.MaxAudioDuration = time.Minute
	reader.config.Timeout = 10 * time.Second
	reader.fetch = func(_ context.Context, method, target string, _ http.Header, _ []byte) (response, error) {
		if method != http.MethodPost || !strings.Contains(target, "youtubei/v1/player") {
			t.Fatalf("unexpected request %s %s", method, target)
		}
		return response{status: http.StatusOK, contentType: "application/json", body: []byte(`{"videoDetails":{"title":"Sample"},"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[]}}}`)}, nil
	}
	result, err := reader.Read(context.Background(), "https://youtu.be/abcdef")
	if err != nil || result.Source != "youtube_audio_transcription" || result.Title != "Sample" || result.Content != "local transcript" {
		t.Fatalf("YouTube fallback result=%#v err=%v", result, err)
	}
}

func writeURLCommand(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
