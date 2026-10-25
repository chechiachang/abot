package media

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveAndReadBoundedPrivateImageAndText(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "media"), Limits{MaxBytes: 1024, MaxExtractBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	image, err := store.Save(context.Background(), "../sample.png", bytes.NewReader([]byte("\x89PNG\r\n\x1a\n")))
	if err != nil {
		t.Fatal(err)
	}
	if image.Name != "sample.png" || image.Kind != "image" || image.MIMEType != "image/png" {
		t.Fatalf("image metadata=%#v", image)
	}
	info, err := os.Stat(image.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private file mode=%v err=%v", info, err)
	}
	_, part, err := store.Extract(context.Background(), image)
	if err != nil || part == nil || string(part.InlineData.Data) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("image part=%#v err=%v", part, err)
	}

	text, err := store.Save(context.Background(), "note.txt", strings.NewReader("local text"))
	if err != nil {
		t.Fatal(err)
	}
	got, part, err := store.Extract(context.Background(), text)
	if err != nil || part != nil || got != "local text" {
		t.Fatalf("text=%q part=%#v err=%v", got, part, err)
	}
}

func TestLocalDocumentConversionPageBoundsAndScannedPDFRejection(t *testing.T) {
	root := t.TempDir()
	pdfinfo := writeCommand(t, root, "pdfinfo", "printf 'Pages: 51\\n'")
	textCommand := writeCommand(t, root, "pdftotext", "touch \"$1.called\"; printf text")
	store, err := NewStore(filepath.Join(root, "media"), Limits{MaxPages: 50, MaxExtractBytes: 1024, PDFInfo: pdfinfo, PDFToText: textCommand})
	if err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(root, "large.pdf")
	if err := os.WriteFile(pdf, []byte("pdf"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Extract(context.Background(), Attachment{Path: pdf, Kind: "pdf"})
	if mediaCode(err) != "too_many_pages" {
		t.Fatalf("page-limit error=%v", err)
	}
	if _, err := os.Stat(pdf + ".called"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pdftotext ran before page count validation: %v", err)
	}

	pdfinfo = writeCommand(t, root, "pdfinfo-empty", "printf 'Pages: 1\\n'")
	textCommand = writeCommand(t, root, "pdftotext-empty", "printf ''")
	store, err = NewStore(filepath.Join(root, "media2"), Limits{MaxPages: 50, MaxExtractBytes: 1024, PDFInfo: pdfinfo, PDFToText: textCommand})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Extract(context.Background(), Attachment{Path: pdf, Kind: "pdf"})
	if mediaCode(err) != "ocr_unavailable" || !strings.Contains(err.Error(), "scanned documents") {
		t.Fatalf("scanned PDF error=%v", err)
	}
}

func TestLocalDocumentAndAudioLimits(t *testing.T) {
	root := t.TempDir()
	pandoc := writeCommand(t, root, "pandoc", "printf '# converted'")
	store, err := NewStore(filepath.Join(root, "media"), Limits{MaxExtractBytes: 1024, Pandoc: pandoc})
	if err != nil {
		t.Fatal(err)
	}
	text, _, err := store.Extract(context.Background(), Attachment{Path: "unused.docx", Kind: "document"})
	if err != nil || text != "# converted" {
		t.Fatalf("document text=%q err=%v", text, err)
	}

	ffprobe := writeCommand(t, root, "ffprobe", "printf '301.0\\n'")
	store, err = NewStore(filepath.Join(root, "audio"), Limits{MaxDuration: 5 * time.Minute, MaxExtractBytes: 1024, FFprobe: ffprobe, FFmpeg: "must-not-run", Whisper: "must-not-run", WhisperModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Extract(context.Background(), Attachment{Path: "unused.ogg", Kind: "audio"})
	if mediaCode(err) != "too_long" {
		t.Fatalf("audio duration error=%v", err)
	}
}

func TestSaveAndExtractReturnStructuredLimitsAndUnavailableErrors(t *testing.T) {
	store, err := NewStore(t.TempDir(), Limits{MaxBytes: 1024, MaxExtractBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(context.Background(), "program.exe", strings.NewReader("x")); mediaCode(err) != "unsupported_media" {
		t.Fatalf("unsupported type error=%v", err)
	}
	if _, err := store.Save(context.Background(), "image.jpg", bytes.NewReader(bytes.Repeat([]byte("x"), 1025))); mediaCode(err) != "too_large" {
		t.Fatalf("oversized error=%v", err)
	}
	audio, err := store.Save(context.Background(), "voice.ogg", strings.NewReader("OggS"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Extract(context.Background(), audio); mediaCode(err) != "transcription_unavailable" {
		t.Fatalf("disabled transcription error=%v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Save(cancelled, "note.txt", strings.NewReader("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upload error=%v", err)
	}
}

func mediaCode(err error) string {
	var mediaErr *Error
	if errors.As(err, &mediaErr) {
		return mediaErr.Code
	}
	return ""
}

func writeCommand(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
