package telegram

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chechiachang/abot/internal/media"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
	"google.golang.org/genai"
)

type mediaFile struct {
	id       string
	name     string
	size     int64
	duration int
}

func (b *Bridge) saveMessageAttachments(ctx context.Context, message *Message, ownerID string, indexedChannel bool) ([]agentruntime.Attachment, error) {
	files := messageMediaFiles(message)
	if indexedChannel {
		files = nil
		if len(message.Photo) > 0 {
			photo := message.Photo[len(message.Photo)-1]
			files = append(files, mediaFile{id: photo.FileID, name: "channel-image.jpg", size: photo.FileSize})
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	if b.Media == nil {
		return nil, errors.New("media processing is not configured")
	}
	if len(files) > 10 {
		return nil, errors.New("a message may contain at most 10 attachments")
	}
	chatKey := strconv.FormatInt(message.Chat.ID, 10)
	attachments := make([]agentruntime.Attachment, 0, len(files))
	var totalBytes int64
	cleanup := func() {
		ids := make([]string, 0, len(attachments))
		for _, item := range attachments {
			ids = append(ids, item.ID)
			_ = os.Remove(item.Path)
		}
		if len(ids) > 0 {
			_ = b.State.RemoveAttachments(context.Background(), ids, chatKey, ownerID)
		}
	}
	for _, item := range files {
		if item.id == "" {
			cleanup()
			return nil, errors.New("Telegram message omitted its media file ID")
		}
		if item.size > 0 && item.size > b.Config.Media.MaxBytes-totalBytes {
			cleanup()
			return nil, errors.New("an attachment exceeds the configured size limit")
		}
		if err := media.ValidateName(item.name); err != nil {
			cleanup()
			return nil, err
		}
		if item.duration > 0 && time.Duration(item.duration)*time.Second > b.Config.Media.MaxDuration {
			cleanup()
			return nil, &media.Error{Code: "too_long", Message: "The audio exceeds the configured duration limit."}
		}
		remote, err := b.Client.GetFile(ctx, item.id)
		if err != nil {
			cleanup()
			return nil, errors.New("could not locate a Telegram attachment")
		}
		remaining := b.Config.Media.MaxBytes - totalBytes
		if remaining <= 0 || remote.FileSize > 0 && remote.FileSize > remaining {
			cleanup()
			return nil, errors.New("an attachment exceeds the configured size limit")
		}
		data, err := b.Client.DownloadFile(ctx, remote.FilePath, remaining)
		if err != nil {
			cleanup()
			return nil, errors.New("could not download an attachment within the configured limit")
		}
		totalBytes += int64(len(data))
		stored, err := b.Media.Save(ctx, item.name, bytes.NewReader(data))
		if err != nil {
			cleanup()
			return nil, err
		}
		record := agentruntime.Attachment{
			ID: stored.ID, ChatID: chatKey, OwnerID: ownerID, SourceMessageID: message.MessageID,
			IndexedChannel: indexedChannel, Path: stored.Path, Name: stored.Name, MIMEType: stored.MIMEType,
			Kind: stored.Kind, Size: stored.Size, CreatedAt: stored.CreatedAt,
		}
		if err := b.State.SaveAttachment(ctx, record); err != nil {
			_ = os.Remove(stored.Path)
			cleanup()
			return nil, errors.New("attachment metadata could not be saved")
		}
		attachments = append(attachments, record)
	}
	return attachments, nil
}

func messageMediaFiles(message *Message) []mediaFile {
	var files []mediaFile
	if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		files = append(files, mediaFile{id: photo.FileID, name: "photo.jpg", size: photo.FileSize})
	}
	if message.Document != nil {
		name := message.Document.FileName
		if name == "" {
			name = nameFromMIME(message.Document.MIMEType)
		}
		files = append(files, mediaFile{id: message.Document.FileID, name: name, size: message.Document.FileSize})
	}
	if message.Voice != nil {
		name := message.Voice.FileName
		if name == "" {
			name = "voice.ogg"
		}
		files = append(files, mediaFile{id: message.Voice.FileID, name: name, size: message.Voice.FileSize, duration: message.Voice.Duration})
	}
	if message.Audio != nil {
		name := message.Audio.FileName
		if name == "" {
			name = nameFromMIME(message.Audio.MIMEType)
		}
		files = append(files, mediaFile{id: message.Audio.FileID, name: name, size: message.Audio.FileSize, duration: message.Audio.Duration})
	}
	return files
}

func nameFromMIME(mimeType string) string {
	switch mimeType {
	case "application/pdf":
		return "attachment.pdf"
	case "text/plain":
		return "attachment.txt"
	case "text/markdown":
		return "attachment.md"
	case "audio/ogg", "audio/opus":
		return "audio.ogg"
	case "audio/mpeg":
		return "audio.mp3"
	case "audio/mp4", "audio/x-m4a":
		return "audio.m4a"
	case "image/jpeg":
		return "image.jpg"
	case "image/png":
		return "image.png"
	case "image/webp":
		return "image.webp"
	default:
		return filepath.Base("unsupported.bin")
	}
}

func (b *Bridge) attachmentInput(ctx context.Context, input agentruntime.QueuedInput, prompt string) (string, []*genai.Part, error) {
	currentPolicy, err := b.policy()
	if err != nil {
		return "", nil, err
	}
	attachments, err := b.State.InputAttachments(ctx, input.ID, input.ChatID, strconv.FormatInt(input.SenderID, 10), currentPolicy.Telegram.ApprovedChannelIDs)
	if err != nil {
		return "", nil, err
	}
	if len(attachments) == 0 {
		return prompt, nil, nil
	}
	if b.Media == nil {
		return "", nil, errors.New("media processing is not configured")
	}
	var imageParts []*genai.Part
	var extracted []string
	var extractedBytes int
	for _, record := range attachments {
		item := media.Attachment{ID: record.ID, Path: record.Path, Name: record.Name, MIMEType: record.MIMEType, Kind: record.Kind, Size: record.Size, CreatedAt: record.CreatedAt}
		text, image, err := b.Media.Extract(ctx, item)
		if err != nil {
			return "", nil, err
		}
		if text != "" {
			extractedBytes += len(text)
			if extractedBytes > b.Config.Media.MaxExtractBytes {
				return "", nil, &media.Error{Code: "too_large", Message: "Extracted attachment text exceeds the configured output limit."}
			}
			extracted = append(extracted, "[Untrusted attachment content: "+safeAttachmentName(record.Name)+"]\n"+text)
		}
		if image != nil {
			imageParts = append(imageParts, image)
		}
	}
	if len(extracted) > 0 {
		prompt += "\n\n" + strings.Join(extracted, "\n\n")
	}
	return prompt, imageParts, nil
}

func safeAttachmentName(name string) string {
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r < 32 {
			return '_'
		}
		return r
	}, name)
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

func mediaFailureMessage(err error) string {
	var mediaErr *media.Error
	if errors.As(err, &mediaErr) {
		return mediaErr.Message
	}
	return "The attachment could not be processed."
}

func (b *Bridge) discardAttachments(ctx context.Context, attachments []agentruntime.Attachment) {
	ids := make([]string, 0, len(attachments))
	for _, item := range attachments {
		ids = append(ids, item.ID)
		_ = os.Remove(item.Path)
	}
	if len(ids) > 0 {
		_ = b.State.RemoveAttachments(ctx, ids, attachments[0].ChatID, attachments[0].OwnerID)
	}
}

func (b *Bridge) expireAttachments(ctx context.Context) error {
	retention := b.Config.Media.Retention
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	expired, err := b.State.ExpireAttachments(ctx, time.Now().UTC().Add(-retention))
	if err != nil {
		return err
	}
	for _, item := range expired {
		_ = os.Remove(item.Path)
	}
	return nil
}
