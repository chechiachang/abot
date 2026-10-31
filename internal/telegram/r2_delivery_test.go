package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chechiachang/abot/internal/config"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
)

func TestR2OutboxRetryRestartAndExpiry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	state, err := agentruntime.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	f := &fakeR2{}
	var messages []SendMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sent SendMessage
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		messages = append(messages, sent)
		if len(messages) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `{"ok":false,"error_code":500,"description":"test delivery failure"}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d,"chat":{"id":42,"type":"private"}}}`, 900+len(messages))
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	bridge := Bridge{State: state, Client: client, R2: testR2(f)}
	input := agentruntime.QueuedInput{ChatID: "42", UserID: "42", BranchID: "branch", ReplyToMessageID: 88}
	item, err := bridge.queueAnswer(ctx, input, "checkpoint", strings.Repeat("中", 1001))
	if err != nil || item.R2ObjectLocator == "" || f.signs != 0 || strings.Contains(item.Payload, "X-Amz") {
		t.Fatalf("queue=%#v err=%v", item, err)
	}
	if _, err := bridge.deliverOne(ctx, item); err == nil {
		t.Fatal("expected Telegram failure")
	}
	// Wait for the retry deadline, then reopen SQLite to exercise recovery.
	time.Sleep(1100 * time.Millisecond)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = agentruntime.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	bridge.State = state
	pending, err := state.PendingOutbox(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	if pending[0].Attempts != 1 || pending[0].R2ObjectLocator != item.R2ObjectLocator || !pending[0].R2ObjectExpiresAt.Equal(item.R2ObjectExpiresAt) {
		t.Fatal("R2 retry metadata not restored")
	}
	if _, err := bridge.deliverOne(ctx, pending[0]); err != nil {
		t.Fatal(err)
	}
	if f.signs != 2 || !strings.Contains(messages[0].Text, "secret-1") || !strings.Contains(messages[1].Text, "secret-2") || !strings.Contains(messages[1].Text, "responses_for_users/(part).txt?X-Amz-Signature=secret-2&amp;") {
		t.Fatalf("links not freshly signed: %#v", messages)
	}
	delivered, err := state.OutboxByMessageID(ctx, 42, 902)
	if err != nil || strings.Contains(delivered.Payload, "X-Amz") || delivered.CheckpointEventID != "checkpoint" || delivered.BranchID != "branch" || delivered.ReplyToMessageID != 88 {
		t.Fatalf("stored delivery=%#v err=%v", delivered, err)
	}

	// A link near lifecycle deletion must not outlive the object.
	near := item
	near.ID = ""
	near.R2ObjectExpiresAt = time.Now().Add(10 * time.Second)
	near, err = state.EnqueueOutbox(ctx, near)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bridge.deliverOne(ctx, near); err != nil {
		t.Fatal(err)
	}
	if f.expiry <= 0 || f.expiry > 10*time.Second {
		t.Fatalf("uncapped expiry %v", f.expiry)
	}
	// After expiration, send an honest notice rather than another signed link.
	expired := item
	expired.ID = ""
	expired.R2ObjectExpiresAt = time.Now().Add(-time.Second)
	expired, err = state.EnqueueOutbox(ctx, expired)
	if err != nil {
		t.Fatal(err)
	}
	signs := f.signs
	if _, err = bridge.deliverOne(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if f.signs != signs || !strings.Contains(messages[len(messages)-1].Text, "expired") {
		t.Fatal("expired object was signed")
	}
}

func TestR2QueueFallbackAndSigningFailures(t *testing.T) {
	ctx := context.Background()
	state, err := agentruntime.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	f := &fakeR2{}
	publisher := &countingArticlePublisher{}
	bridge := Bridge{State: state, R2: testR2(f), Publisher: publisher, Config: config.Config{Telegram: config.TelegramConfig{ResponseInlineThreshold: 2000}}}
	input := agentruntime.QueuedInput{ChatID: "42"}
	inline, err := bridge.queueAnswer(ctx, input, "e", strings.Repeat("x", 1500))
	if err != nil || inline.R2ObjectLocator != "" || publisher.calls != 0 {
		t.Fatal("custom inline preference ignored")
	}
	bridge.R2 = nil
	morsel, err := bridge.queueAnswer(ctx, input, "e", strings.Repeat("x", 1001))
	if err != nil || publisher.calls != 1 || !strings.Contains(morsel.Payload, "morsel.example") {
		t.Fatal("legacy Morsel path bypassed")
	}
	bridge.Publisher = nil
	missing, err := bridge.queueAnswer(ctx, input, "e", strings.Repeat("x", 1001))
	if err != nil || !strings.Contains(missing.Payload, "MORSEL_API_KEY") {
		t.Fatal("missing publisher fallback changed")
	}
	bridge.R2 = testR2(f)
	f.putErr = errors.New("secret")
	failed, err := bridge.queueAnswer(ctx, input, "checkpoint", strings.Repeat("x", 2001))
	if err != nil || failed.R2ObjectLocator != "" || strings.Contains(failed.Payload, "secret") || !strings.Contains(failed.Payload, "shorter") || failed.CheckpointEventID != "checkpoint" {
		t.Fatal("unsafe upload fallback")
	}
	f.putErr = nil
	f.signErr = errors.New("secret")
	signing, err := bridge.queueAnswer(ctx, input, "e", strings.Repeat("x", 2001))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bridge.deliverOne(ctx, signing); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe signing error %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	pending, err := state.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range pending {
		if item.ID != signing.ID {
			continue
		}
		bridge.R2 = nil
		if _, err = bridge.deliverOne(ctx, item); err == nil {
			t.Fatal("missing R2 was not recorded as failure")
		}
	}
	pending, err = state.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range pending {
		if item.ID == signing.ID {
			t.Fatal("permanent missing-config failure retried")
		}
	}
}
