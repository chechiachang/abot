package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	ImportFormatVersion = 1
	maxImportBytes      = 64 << 20
	maxImportLineBytes  = 1 << 20
	maxImportRecords    = 100_000
)

type ImportEntry struct {
	FormatVersion int       `json:"format_version"`
	ChatID        string    `json:"chat_id"`
	UserID        string    `json:"user_id"`
	SessionID     string    `json:"session_id"`
	EventID       string    `json:"event_id"`
	Timestamp     time.Time `json:"timestamp"`
	Author        string    `json:"author"`
	Role          string    `json:"role"`
	Branch        string    `json:"branch"`
	Text          string    `json:"text"`
}

func ValidateJSONL(data []byte, source string) (ImportRun, error) {
	if strings.TrimSpace(source) == "" {
		return ImportRun{}, errors.New("import source name is required")
	}
	report := ImportRun{FormatVersion: ImportFormatVersion, Source: filepath.Base(source), DryRun: true, CreatedAt: time.Now().UTC()}
	if len(data) == 0 || len(data) > maxImportBytes {
		return report, fmt.Errorf("import file must be between 1 byte and %d bytes", maxImportBytes)
	}
	entries, failures := parseImportJSONL(data)
	report.Failed = failures
	if failures > 0 {
		return report, nil
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		key := entry.UserID + "\x00" + entry.SessionID + "\x00" + entry.EventID
		if seen[key] {
			report.Skipped++
			continue
		}
		seen[key] = true
		report.Imported++
	}
	return report, nil
}

func (s *Store) ImportJSONL(ctx context.Context, data []byte, source string, apply bool) (ImportRun, error) {
	if apply {
		if err := s.requireWrite(); err != nil {
			return ImportRun{}, err
		}
	}
	if strings.TrimSpace(source) == "" {
		return ImportRun{}, errors.New("import source name is required")
	}
	report := ImportRun{ID: uuid.NewString(), FormatVersion: ImportFormatVersion, Source: filepath.Base(source), DryRun: !apply, CreatedAt: time.Now().UTC()}
	if len(data) == 0 || len(data) > maxImportBytes {
		return report, fmt.Errorf("import file must be between 1 byte and %d bytes", maxImportBytes)
	}
	entries, parseFailures := parseImportJSONL(data)
	report.Failed = parseFailures
	if parseFailures > 0 {
		return report, nil
	}
	grouped := make(map[string][]ImportEntry)
	order := make([]string, 0)
	inputIDs := make(map[string]map[string]bool)
	for _, entry := range entries {
		key := strings.Join([]string{entry.UserID, entry.SessionID}, "\x00")
		if inputIDs[key] == nil {
			inputIDs[key] = make(map[string]bool)
			order = append(order, key)
		}
		if inputIDs[key][entry.EventID] {
			report.Skipped++
			continue
		}
		inputIDs[key][entry.EventID] = true
		grouped[key] = append(grouped[key], entry)
	}
	sessionsByKey := make(map[string]session.Session, len(grouped))
	existingIDs := make(map[string]map[string]bool, len(grouped))
	plan := make(map[string][]ImportEntry, len(grouped))
	for _, key := range order {
		items := grouped[key]
		if len(items) == 0 {
			continue
		}
		first := items[0]
		response, err := s.sessions.Get(ctx, &session.GetRequest{AppName: "abot", UserID: first.UserID, SessionID: first.SessionID})
		if err == nil {
			sessionsByKey[key] = response.Session
			existingIDs[key] = make(map[string]bool, response.Session.Events().Len())
			for event := range response.Session.Events().All() {
				if event != nil {
					existingIDs[key][event.ID] = true
				}
			}
		} else if !errors.Is(err, session.ErrNotFound) {
			return report, fmt.Errorf("inspect import session %q: %w", first.SessionID, err)
		} else {
			existingIDs[key] = make(map[string]bool)
		}
		for _, entry := range items {
			if existingIDs[key][entry.EventID] {
				report.Skipped++
				continue
			}
			plan[key] = append(plan[key], entry)
			report.Imported++
		}
	}
	if !apply {
		return report, nil
	}
	planned := report.Imported
	report.Imported = 0
	if planned == 0 {
		if err := s.saveImportReport(ctx, report); err != nil {
			return report, err
		}
		return report, nil
	}
	for _, key := range order {
		items := plan[key]
		if len(items) == 0 {
			continue
		}
		first := items[0]
		current := sessionsByKey[key]
		if current == nil {
			created, err := s.sessions.Create(ctx, &session.CreateRequest{AppName: "abot", UserID: first.UserID, SessionID: first.SessionID})
			if err != nil {
				report.Failed += len(items)
				continue
			}
			current = created.Session
		}
		for _, entry := range items {
			role := genai.Role(genai.RoleUser)
			if entry.Role == "model" {
				role = genai.RoleModel
			}
			event := &session.Event{
				ID: entry.EventID, Timestamp: entry.Timestamp.UTC(), Author: entry.Author, Branch: entry.Branch,
				Actions:     session.EventActions{StateDelta: map[string]any{}},
				LLMResponse: modelResponse(genai.NewContentFromText(entry.Text, role)),
			}
			if err := s.sessions.AppendEvent(ctx, current, event); err != nil {
				report.Failed++
				continue
			}
			if err := s.UpdateCheckpoint(ctx, entry.ChatID, entry.Branch, entry.EventID); err != nil {
				report.Failed++
				continue
			}
			report.Imported++
		}
	}
	if err := s.saveImportReport(ctx, report); err != nil {
		return report, err
	}
	return report, nil
}

func parseImportJSONL(data []byte) ([]ImportEntry, int) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxImportLineBytes)
	entries := make([]ImportEntry, 0)
	failures := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if len(entries)+failures >= maxImportRecords {
			return entries, failures + 1
		}
		var entry ImportEntry
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&entry); err != nil {
			failures++
			continue
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF || !validImportEntry(entry) {
			failures++
			continue
		}
		entries = append(entries, entry)
	}
	if scanner.Err() != nil {
		failures++
	}
	return entries, failures
}

func validImportEntry(entry ImportEntry) bool {
	return entry.FormatVersion == ImportFormatVersion && entry.ChatID != "" && entry.UserID != "" && entry.SessionID != "" && entry.EventID != "" &&
		!entry.Timestamp.IsZero() && entry.Author != "" && (entry.Role == "user" || entry.Role == "model") && entry.Branch != "" && len(entry.Text) <= maxImportLineBytes
}

func (s *Store) saveImportReport(ctx context.Context, report ImportRun) error {
	if err := s.db.WithContext(ctx).Create(&report).Error; err != nil {
		return fmt.Errorf("save import report: %w", err)
	}
	return nil
}

func modelResponse(content *genai.Content) model.LLMResponse {
	return model.LLMResponse{Content: content, TurnComplete: true}
}
