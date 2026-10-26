package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestTelegramUpdateDeduplicationAndDurableOffset(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	accepted, err := store.RecordTelegramUpdate(ctx, 41, -100, "queued")
	if err != nil || !accepted {
		t.Fatalf("first update accepted=%t err=%v", accepted, err)
	}
	accepted, err = store.RecordTelegramUpdate(ctx, 41, -100, "duplicate")
	if err != nil || accepted {
		t.Fatalf("duplicate update accepted=%t err=%v", accepted, err)
	}
	offset, err := store.TelegramUpdateOffset(ctx)
	if err != nil || offset != 42 {
		t.Fatalf("poll offset=%d err=%v", offset, err)
	}
}

func TestAttachmentOwnershipAndApprovedChannelReadAuthorization(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SaveAttachment(ctx, Attachment{ID: "channel-image", ChatID: "-900", SourceMessageID: 22, IndexedChannel: true, Path: "/private/image.png", Name: "image.png", MIMEType: "image/png", Kind: "image", Size: 10, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	input, err := store.EnqueueTelegramTurn(ctx, "10", "main", "10", 10, 1, 0, InputFollowup, "describe")
	if err != nil {
		t.Fatal(err)
	}
	attachments, err := store.InputAttachments(ctx, input.ID, "10", "10", nil)
	if err != nil || len(attachments) != 0 {
		t.Fatalf("text-only input attachments=%#v err=%v", attachments, err)
	}
	if err := store.AttachInput(ctx, input.ID, "10", "10", []string{"channel-image"}, []int64{-900}); err != nil {
		t.Fatal(err)
	}
	allowed, err := store.InputAttachments(ctx, input.ID, "10", "10", []int64{-900})
	if err != nil || len(allowed) != 1 || allowed[0].ID != "channel-image" {
		t.Fatalf("approved channel attachment=%#v err=%v", allowed, err)
	}
	denied, err := store.InputAttachments(ctx, input.ID, "10", "10", nil)
	if err == nil || len(denied) != 0 {
		t.Fatalf("unapproved channel attachment=%#v err=%v", denied, err)
	}
	if err := store.SaveAttachment(ctx, Attachment{ID: "private-image", ChatID: "10", OwnerID: "10", Path: "/private/own.png", Name: "own.png", MIMEType: "image/png", Kind: "image", Size: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachInput(ctx, input.ID, "10", "other-user", []string{"private-image"}, nil); err == nil {
		t.Fatal("expected cross-user attachment reference to be denied")
	}
}

func TestTelegramOutboxRetriesAfterRestartAndMapsAnswerMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	item, err := store.EnqueueOutbox(ctx, Outbox{ChatID: 99, UserID: "99", BranchID: "main", CheckpointEventID: "event-1", Payload: "answer", ParseMode: "HTML"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueOutbox(ctx, Outbox{ChatID: 99, Payload: "another"}); err != nil {
		t.Fatalf("multiple pending outbox items: %v", err)
	}
	started, err := store.BeginDelivery(ctx, item.ID)
	if err != nil || !started {
		t.Fatalf("begin delivery=%t err=%v", started, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pending, err := store.PendingOutbox(ctx, 10)
	if err != nil || len(pending) != 2 || pending[0].Status != OutboxPending || pending[0].Attempts != 1 {
		t.Fatalf("recovered outbox=%#v err=%v", pending, err)
	}
	started, err = store.BeginDelivery(ctx, item.ID)
	if err != nil || !started {
		t.Fatalf("retry begin=%t err=%v", started, err)
	}
	if err := store.CompleteDelivery(ctx, item.ID, 500); err != nil {
		t.Fatal(err)
	}
	mapped, err := store.OutboxByMessageID(ctx, 99, 500)
	if err != nil || mapped.CheckpointEventID != "event-1" || mapped.BranchID != "main" {
		t.Fatalf("answer mapping=%#v err=%v", mapped, err)
	}
}

func TestTelegramOutboxMessageReuseAndIndexMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := gorm.Open(sqlite.Open(sqliteDSN(path, false)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Outbox{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP INDEX idx_runtime_outbox_chat_message").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_runtime_telegram_outbox_telegram_message_id ON runtime_telegram_outbox(telegram_message_id)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_runtime_outbox_chat_message ON runtime_telegram_outbox(chat_id, telegram_message_id)").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for i, chatID := range []int64{10, 20, 10} {
		eventID := []string{"event-a", "event-b", "event-c"}[i]
		item, err := store.EnqueueOutbox(ctx, Outbox{ChatID: chatID, CheckpointEventID: eventID, Payload: "answer"})
		if err != nil {
			t.Fatal(err)
		}
		if started, err := store.BeginDelivery(ctx, item.ID); err != nil || !started {
			t.Fatalf("begin delivery for chat %d = %t, err=%v", chatID, started, err)
		}
		if err := store.CompleteDelivery(ctx, item.ID, 42); err != nil {
			t.Fatalf("complete delivery for chat %d: %v", chatID, err)
		}
		mapped, err := store.OutboxByMessageID(ctx, chatID, 42)
		if err != nil || mapped.ChatID != chatID || (i == 2 && mapped.CheckpointEventID != eventID) {
			t.Fatalf("message mapping for chat %d = %#v, err=%v", chatID, mapped, err)
		}
	}
}

func TestAccessRequestReviewAndResetKeepPreviousHistory(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	request, created, err := store.RequestAccess(ctx, 7, 7, "private")
	if err != nil || !created {
		t.Fatalf("request=%#v created=%t err=%v", request, created, err)
	}
	if _, created, err := store.RequestAccess(ctx, 7, 7, "private"); err != nil || created {
		t.Fatalf("duplicate pending request created=%t err=%v", created, err)
	}
	if err := store.ReviewAccessRequest(ctx, request.ID, "admin", true); err != nil {
		t.Fatal(err)
	}
	requests, err := store.ListAccessRequests(ctx, "approved")
	if err != nil || len(requests) != 1 || requests[0].ReviewedBy != "admin" {
		t.Fatalf("reviewed requests=%#v err=%v", requests, err)
	}
	if err := store.ActivateBranch(ctx, "7", "main"); err != nil {
		t.Fatal(err)
	}
	branch, err := store.ResetBranch(ctx, "7", "7")
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.CurrentBranch(ctx, "7")
	if err != nil || current != branch.BranchID || branch.ParentBranchID != "main" {
		t.Fatalf("reset branch=%#v current=%s err=%v", branch, current, err)
	}
}
