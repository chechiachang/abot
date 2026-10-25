package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestHistoricalJSONLImportDryRunApplyAndDuplicateHandling(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	created, err := store.Sessions().Create(ctx, &session.CreateRequest{AppName: "abot", UserID: "user-1", SessionID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions().AppendEvent(ctx, created.Session, &session.Event{ID: "existing", Timestamp: time.Now(), Author: "user", LLMResponse: model.LLMResponse{Content: modelContent("already here", genai.RoleUser)}}); err != nil {
		t.Fatal(err)
	}
	data := importLines(t, []ImportEntry{
		importEntry("existing", "duplicate source entry"),
		importEntry("new-event", "new conversation entry"),
	})
	dryRun, err := store.ImportJSONL(ctx, data, "/tmp/history.jsonl", false)
	if err != nil || !dryRun.DryRun || dryRun.Imported != 1 || dryRun.Skipped != 1 || dryRun.Failed != 0 {
		t.Fatalf("dry-run report=%#v err=%v", dryRun, err)
	}
	loaded, err := store.Sessions().Get(ctx, &session.GetRequest{AppName: "abot", UserID: "user-1", SessionID: "chat-1"})
	if err != nil || loaded.Session.Events().Len() != 1 {
		t.Fatalf("dry-run mutated session: events=%v err=%v", loaded, err)
	}
	applied, err := store.ImportJSONL(ctx, data, "history.jsonl", true)
	if err != nil || applied.DryRun || applied.Imported != 1 || applied.Skipped != 1 || applied.Failed != 0 {
		t.Fatalf("apply report=%#v err=%v", applied, err)
	}
	loaded, err = store.Sessions().Get(ctx, &session.GetRequest{AppName: "abot", UserID: "user-1", SessionID: "chat-1"})
	if err != nil || loaded.Session.Events().Len() != 2 || loaded.Session.Events().At(1).ID != "new-event" {
		t.Fatalf("imported session events=%v err=%v", loaded, err)
	}
	replayed, err := store.ImportJSONL(ctx, data, "history.jsonl", true)
	if err != nil || replayed.Imported != 0 || replayed.Skipped != 2 {
		t.Fatalf("duplicate apply report=%#v err=%v", replayed, err)
	}
	var reports []ImportRun
	if err := store.db.Find(&reports).Error; err != nil || len(reports) != 2 {
		t.Fatalf("persisted import reports=%#v err=%v", reports, err)
	}
}

func TestHistoricalJSONLImportRejectsInvalidInputBeforeMutation(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	valid := importEntry("valid", "must not import")
	data := append(importLines(t, []ImportEntry{valid}), []byte("{invalid json}\n")...)
	report, err := store.ImportJSONL(context.Background(), data, "history.jsonl", true)
	if err != nil || report.Failed != 1 || report.Imported != 0 {
		t.Fatalf("invalid import report=%#v err=%v", report, err)
	}
	_, err = store.Sessions().Get(context.Background(), &session.GetRequest{AppName: "abot", UserID: valid.UserID, SessionID: valid.SessionID})
	if err == nil {
		t.Fatal("invalid import mutated session state")
	}
	if strings.Contains(err.Error(), "must not import") {
		t.Fatalf("error exposed source content: %v", err)
	}
}

func importEntry(id, text string) ImportEntry {
	return ImportEntry{FormatVersion: ImportFormatVersion, ChatID: "chat-1", UserID: "user-1", SessionID: "chat-1", EventID: id, Timestamp: time.Date(2030, 10, 25, 0, 0, 0, 0, time.UTC), Author: "user", Role: "user", Branch: "main", Text: text}
}

func importLines(t *testing.T, entries []ImportEntry) []byte {
	t.Helper()
	var data strings.Builder
	for _, entry := range entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		data.Write(encoded)
		data.WriteByte('\n')
	}
	return []byte(data.String())
}

func modelContent(text string, role genai.Role) *genai.Content {
	return genai.NewContentFromText(text, role)
}
