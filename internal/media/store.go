package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genai"
)

const defaultExtractLimit = 1 << 20

type Limits struct {
	MaxBytes        int64
	MaxDuration     time.Duration
	MaxPages        int
	MaxExtractBytes int
	Pandoc          string
	PDFInfo         string
	PDFToText       string
	FFmpeg          string
	FFprobe         string
	Whisper         string
	WhisperModel    string
}

type Attachment struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	MIMEType  string    `json:"mime_type"`
	Kind      string    `json:"kind"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	root   string
	limits Limits
}

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func NewStore(root string, limits Limits) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("media artifact directory is required")
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 10 << 20
	}
	if limits.MaxDuration <= 0 {
		limits.MaxDuration = 5 * time.Minute
	}
	if limits.MaxPages <= 0 {
		limits.MaxPages = 50
	}
	if limits.MaxExtractBytes <= 0 {
		limits.MaxExtractBytes = defaultExtractLimit
	}
	if limits.MaxExtractBytes > 10<<20 {
		return nil, errors.New("media extracted-text limit cannot exceed 10 MiB")
	}
	if strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve media home directory: %w", err)
		}
		root = filepath.Join(home, strings.TrimPrefix(root, "~/"))
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve media directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, fmt.Errorf("create media directory: %w", err)
	}
	if err := os.Chmod(abs, 0700); err != nil {
		return nil, fmt.Errorf("restrict media directory permissions: %w", err)
	}
	return &Store{root: abs, limits: limits}, nil
}

func (s *Store) Save(ctx context.Context, name string, input io.Reader) (Attachment, error) {
	if s == nil || input == nil {
		return Attachment{}, errors.New("media store and input are required")
	}
	if err := ctx.Err(); err != nil {
		return Attachment{}, err
	}
	ext := strings.ToLower(filepath.Ext(filepath.Base(name)))
	allowed, mimeType, kind := mediaType(ext)
	if !allowed {
		return Attachment{}, mediaError("unsupported_media", "This file type is not supported.")
	}
	id := uuid.NewString()
	temp, err := os.CreateTemp(s.root, ".incoming-*")
	if err != nil {
		return Attachment{}, fmt.Errorf("create private media file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return Attachment{}, err
	}
	buffer := make([]byte, 64<<10)
	var size int64
	var prefix []byte
	for {
		if err := ctx.Err(); err != nil {
			temp.Close()
			return Attachment{}, err
		}
		count, readErr := input.Read(buffer)
		if count > 0 {
			size += int64(count)
			if size > s.limits.MaxBytes {
				temp.Close()
				return Attachment{}, mediaError("too_large", "The attachment exceeds the configured size limit.")
			}
			if len(prefix) < 512 {
				prefix = append(prefix, buffer[:min(count, 512-len(prefix))]...)
			}
			if _, err := temp.Write(buffer[:count]); err != nil {
				temp.Close()
				return Attachment{}, fmt.Errorf("write private media file: %w", err)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				temp.Close()
				return Attachment{}, fmt.Errorf("read attachment: %w", readErr)
			}
			break
		}
	}
	if size == 0 {
		temp.Close()
		return Attachment{}, mediaError("empty_media", "The attachment is empty.")
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return Attachment{}, err
	}
	if err := temp.Close(); err != nil {
		return Attachment{}, err
	}
	if strings.HasPrefix(kind, "image") {
		detected := http.DetectContentType(prefix)
		if !strings.HasPrefix(detected, mimeType) {
			return Attachment{}, mediaError("invalid_media", "The image contents do not match the declared file type.")
		}
	}
	path := filepath.Join(s.root, id+ext)
	if err := os.Rename(tempPath, path); err != nil {
		return Attachment{}, fmt.Errorf("store private media file: %w", err)
	}
	return Attachment{ID: id, Path: path, Name: filepath.Base(name), MIMEType: mimeType, Kind: kind, Size: size, CreatedAt: time.Now().UTC()}, nil
}

func (s *Store) Extract(ctx context.Context, item Attachment) (string, *genai.Part, error) {
	switch item.Kind {
	case "image":
		data, err := os.ReadFile(item.Path)
		if err != nil {
			return "", nil, fmt.Errorf("read private image: %w", err)
		}
		if int64(len(data)) > s.limits.MaxBytes {
			return "", nil, mediaError("too_large", "The image exceeds the configured size limit.")
		}
		return "", &genai.Part{InlineData: &genai.Blob{MIMEType: item.MIMEType, Data: data}}, nil
	case "text":
		return readBounded(item.Path, s.limits.MaxExtractBytes)
	case "pdf":
		return s.extractPDF(ctx, item.Path)
	case "document":
		text, part, err := s.runText(ctx, "document_conversion", s.limits.Pandoc, []string{item.Path, "--to", "gfm"})
		if err == nil && text == "" {
			return "", nil, mediaError("no_text", "The document has no extractable text.")
		}
		return text, part, err
	case "audio":
		return s.transcribe(ctx, item)
	default:
		return "", nil, mediaError("unsupported_media", "This attachment type is not supported.")
	}
}

func (s *Store) extractPDF(ctx context.Context, path string) (string, *genai.Part, error) {
	metadata, _, err := s.runText(ctx, "document_conversion", s.limits.PDFInfo, []string{path})
	if err != nil {
		return "", nil, err
	}
	pages := 0
	for _, line := range strings.Split(metadata, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if strings.TrimSpace(key) == "Pages" && ok {
			pages, _ = strconv.Atoi(strings.TrimSpace(value))
			break
		}
	}
	if pages < 1 {
		return "", nil, mediaError("document_conversion", "Could not determine the PDF page count; install Poppler tools.")
	}
	if pages > s.limits.MaxPages {
		return "", nil, mediaError("too_many_pages", "The PDF exceeds the configured page limit.")
	}
	text, part, err := s.runText(ctx, "document_conversion", s.limits.PDFToText, []string{"-layout", path, "-"})
	if err == nil && text == "" {
		return "", nil, mediaError("ocr_unavailable", "This PDF has no extractable text; scanned documents requiring OCR are not supported.")
	}
	return text, part, err
}

func (s *Store) transcribe(ctx context.Context, item Attachment) (string, *genai.Part, error) {
	if s.limits.Whisper == "" || s.limits.WhisperModel == "" || s.limits.FFmpeg == "" || s.limits.FFprobe == "" {
		return "", nil, mediaError("transcription_unavailable", "Audio transcription is disabled; configure FFmpeg, FFprobe, and a local Whisper model.")
	}
	durationText, _, err := s.runText(ctx, "audio_probe", s.limits.FFprobe, []string{"-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", item.Path})
	if err != nil {
		return "", nil, err
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(durationText), 64)
	if err != nil || seconds <= 0 {
		return "", nil, mediaError("audio_probe", "Could not determine the audio duration.")
	}
	if time.Duration(seconds*float64(time.Second)) > s.limits.MaxDuration {
		return "", nil, mediaError("too_long", "The audio exceeds the configured duration limit.")
	}
	wav, err := os.CreateTemp(s.root, ".audio-*.wav")
	if err != nil {
		return "", nil, err
	}
	wavPath := wav.Name()
	_ = wav.Close()
	defer os.Remove(wavPath)
	if _, _, err := s.runText(ctx, "audio_conversion", s.limits.FFmpeg, []string{"-nostdin", "-y", "-i", item.Path, "-ac", "1", "-ar", "16000", wavPath}); err != nil {
		return "", nil, err
	}
	return s.runText(ctx, "transcription", s.limits.Whisper, []string{"-m", s.limits.WhisperModel, "-f", wavPath, "-nt", "-np"})
}

func (s *Store) TranscribeAudio(ctx context.Context, path string) (string, error) {
	text, _, err := s.transcribe(ctx, Attachment{Path: path, Kind: "audio"})
	return text, err
}

func (s *Store) runText(ctx context.Context, code, binary string, args []string) (string, *genai.Part, error) {
	if binary == "" {
		return "", nil, mediaError(code, "The required local conversion tool is not configured.")
	}
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(commandCtx, binary, args...)
	var output boundedBuffer
	output.limit = s.limits.MaxExtractBytes
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if commandCtx.Err() != nil {
			return "", nil, mediaError("timeout", "Local media processing timed out or was cancelled.")
		}
		return "", nil, mediaError(code, "Local media processing failed; check the configured converter and file support.")
	}
	if output.overflow {
		return "", nil, mediaError("too_large", "Extracted media text exceeds the configured output limit.")
	}
	return strings.TrimSpace(output.String()), nil, nil
}

func readBounded(path string, limit int) (string, *genai.Part, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return "", nil, err
	}
	if len(data) > limit {
		return "", nil, mediaError("too_large", "Extracted media text exceeds the configured output limit.")
	}
	return strings.TrimSpace(string(data)), nil, nil
}

func mediaType(ext string) (bool, string, string) {
	switch ext {
	case ".png":
		return true, "image/png", "image"
	case ".jpg", ".jpeg":
		return true, "image/jpeg", "image"
	case ".webp":
		return true, "image/webp", "image"
	case ".txt", ".md", ".csv":
		return true, "text/plain", "text"
	case ".pdf":
		return true, "application/pdf", "pdf"
	case ".docx", ".odt", ".rtf":
		return true, "application/octet-stream", "document"
	case ".ogg", ".oga", ".opus", ".wav", ".mp3", ".m4a", ".aac", ".flac":
		return true, "audio/*", "audio"
	default:
		return false, "", ""
	}
}

func SupportedName(name string) bool {
	allowed, _, _ := mediaType(strings.ToLower(filepath.Ext(filepath.Base(name))))
	return allowed
}

func ValidateName(name string) error {
	if !SupportedName(name) {
		return mediaError("unsupported_media", "This file type is not supported.")
	}
	return nil
}

func mediaError(code, message string) error { return &Error{Code: code, Message: message} }

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return n, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
