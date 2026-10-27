package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestSQLiteSessionsAndRuntimeMetadataSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := store.Sessions().Create(ctx, &session.CreateRequest{AppName: "abot", UserID: "user-10", SessionID: "chat-10"})
	if err != nil {
		t.Fatal(err)
	}
	event := &session.Event{
		ID: "event-1", Timestamp: time.Now().UTC(), InvocationID: "generation-1", Branch: "main", Author: "user",
		LLMResponse: structLLMResponse("hello"),
	}
	if err := store.Sessions().AppendEvent(ctx, created.Session, event); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginTurn(ctx, "chat-10", "main", "generation-1"); err != nil {
		t.Fatal(err)
	}
	recordID, err := store.Record(ctx, "generation-1", "tool", "bash")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetProgress(ctx, "chat-10", "main", "generation-1", []Step{{ID: "inspect", Title: "Inspect repository", Status: StepCompleted}, {ID: "edit", Title: "Edit file", Status: StepInProgress}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.Sessions().Get(ctx, &session.GetRequest{AppName: "abot", UserID: "user-10", SessionID: "chat-10"})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.Events().Len() != 1 || loaded.Session.Events().At(0).ID != "event-1" || loaded.Session.Events().At(0).Content.Parts[0].Text != "hello" {
		t.Fatalf("persisted ADK events = %#v", loaded.Session.Events())
	}
	task, records, steps, err := store.TaskDetails(ctx, "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskUncertain || task.ErrorKind != "process_interrupted" {
		t.Fatalf("recovered generation = %#v", task)
	}
	if len(records) != 1 || records[0].ID != recordID || records[0].Status != RecordUncertain {
		t.Fatalf("recovered tool records = %#v", records)
	}
	if len(steps) != 2 || steps[1].Status != StepInProgress || steps[1].BranchID != "main" {
		t.Fatalf("recovered progress = %#v", steps)
	}
}

func TestActiveTurnIsSerializedPerChatBranchAndChatsAreIsolated(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.BeginTurn(ctx, "chat-a", "main", "a-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginTurn(ctx, "chat-a", "main", "a-2"); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("second generation error = %v", err)
	}
	if err := store.BeginTurn(ctx, "chat-a", "branch-2", "a-3"); err != nil {
		t.Fatalf("different branch should be independent: %v", err)
	}
	if err := store.BeginTurn(ctx, "chat-b", "main", "b-1"); err != nil {
		t.Fatalf("different chat should be independent: %v", err)
	}
}

func TestReadOnlyStoreDoesNotCreateOrMutateRuntimeState(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.sqlite")
	if _, err := OpenReadOnly(missing); err == nil {
		t.Fatal("expected missing database to fail")
	}
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginTurn(context.Background(), "chat", "main", "generation"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.FinishTurn(context.Background(), "generation", TaskCompleted, "event", ""); err == nil {
		t.Fatal("read-only database allowed metadata mutation")
	}
	tasks, err := store.ListTasks(context.Background(), "chat", 10)
	if err != nil || len(tasks) != 1 || tasks[0].Status != TaskRunning {
		t.Fatalf("read-only tasks=%#v err=%v", tasks, err)
	}
}

func TestWritableStoreExclusivelyOwnsRuntimeDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrRuntimeInUse) {
		t.Fatalf("second writer error = %v", err)
	}
	inspector, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("read-only inspector should coexist with writer: %v", err)
	}
	if err := inspector.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := OpenAdmin(path)
	if err != nil {
		t.Fatalf("admin metadata writer should coexist with runtime writer: %v", err)
	}
	if _, _, err := admin.RequestAccess(context.Background(), 5, 5, "private"); err != nil {
		t.Fatalf("admin request update failed: %v", err)
	}
	if err := admin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(path); err != nil {
		t.Fatalf("writer lock was not released: %v", err)
	} else {
		reopened.Close()
	}
}

func TestToolObserverPersistsExecutionStatusWithoutToolPayloads(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.BeginTurn(ctx, "chat", "main", "generation"); err != nil {
		t.Fatal(err)
	}
	finish, err := store.ToolObserver("generation")(ctx, "write")
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	_, records, _, err := store.TaskDetails(ctx, "generation")
	if err != nil || len(records) != 1 || records[0].Name != "write" || records[0].Status != RecordCompleted {
		t.Fatalf("tool records=%#v err=%v", records, err)
	}
}

func TestForkAndCompactionPreserveRecoverableBranchHistory(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	branch, err := store.EnsureBranch(ctx, "chat", "main")
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Sessions().Create(ctx, &session.CreateRequest{AppName: "abot", UserID: "user", SessionID: branch.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id, text string
		role     genai.Role
	}{{"event-1", "question one", genai.RoleUser}, {"event-2", "answer one", genai.RoleModel}, {"event-3", "question two", genai.RoleUser}} {
		event := &session.Event{ID: item.id, Timestamp: time.Now().UTC(), Author: "coordinator", Branch: "main", LLMResponse: model.LLMResponse{Content: genai.NewContentFromText(item.text, item.role)}}
		if err := store.Sessions().AppendEvent(ctx, created.Session, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.BeginTurn(ctx, "chat", "main", "generation-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProgress(ctx, "chat", "main", "generation-1", []Step{{ID: "inspect", Title: "Inspect source", Status: StepCompleted}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateCheckpoint(ctx, "chat", "main", "event-2"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishTurn(ctx, "generation-1", TaskCompleted, "event-2", ""); err != nil {
		t.Fatal(err)
	}
	fork, err := store.ForkBranch(ctx, "chat", "user", "main", "reply", "event-2")
	if err != nil {
		t.Fatal(err)
	}
	forked, err := store.Sessions().Get(ctx, &session.GetRequest{AppName: "abot", UserID: "user", SessionID: fork.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if forked.Session.Events().Len() != 2 || forked.Session.Events().At(1).ID != "event-2" {
		t.Fatalf("forked events=%v", eventIDs(forked.Session))
	}
	progress, err := store.BranchProgress(ctx, "chat", "reply")
	if err != nil || len(progress) != 1 || progress[0].Title != "Inspect source" || progress[0].Status != StepCompleted {
		t.Fatalf("forked progress=%#v err=%v", progress, err)
	}
	compacted, err := store.CompactBranch(ctx, "chat", "user", "main", "Prior discussion covered question one and its answer.")
	if err != nil {
		t.Fatal(err)
	}
	if compacted.SessionID == branch.SessionID || !strings.HasPrefix(compacted.CheckpointEventID, "compact-") {
		t.Fatalf("compacted branch=%#v", compacted)
	}
	revisions, err := store.ListBranchRevisions(ctx, "chat", "main")
	if err != nil || len(revisions) != 1 || revisions[0].SessionID != branch.SessionID {
		t.Fatalf("revisions=%#v err=%v", revisions, err)
	}
	current, err := store.Sessions().Get(ctx, &session.GetRequest{AppName: "abot", UserID: "user", SessionID: compacted.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if current.Session.Events().Len() != 1 || current.Session.Events().At(0).Content.Parts[0].Text != "Prior discussion covered question one and its answer." {
		t.Fatalf("compacted events=%v", eventIDs(current.Session))
	}
	forkedOlderCheckpoint, err := store.ForkBranch(ctx, "chat", "user", "main", "older-reply", "event-2")
	if err != nil {
		t.Fatalf("fork from archived event: %v", err)
	}
	archivedFork, err := store.Sessions().Get(ctx, &session.GetRequest{AppName: "abot", UserID: "user", SessionID: forkedOlderCheckpoint.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if archivedFork.Session.Events().Len() != 2 {
		t.Fatalf("archived fork events=%v", eventIDs(archivedFork.Session))
	}
}

func eventIDs(sess session.Session) []string {
	if sess == nil {
		return nil
	}
	ids := make([]string, 0, sess.Events().Len())
	for event := range sess.Events().All() {
		if event != nil {
			ids = append(ids, event.ID)
		}
	}
	return ids
}

func TestQueuedSteeringAndFollowupsRecoverAndClaimInPriorityOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	followup, err := store.EnqueueInput(ctx, "chat", "main", InputFollowup, "later question")
	if err != nil {
		t.Fatal(err)
	}
	steering, err := store.EnqueueInput(ctx, "chat", "main", InputSteering, "change direction")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimInputs(ctx, "chat", "main", 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != steering.ID {
		t.Fatalf("priority claim=%#v err=%v", claimed, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	claimed, err = store.ClaimInputs(ctx, "chat", "main", 10)
	if err != nil || len(claimed) != 2 || claimed[0].ID != steering.ID || claimed[1].ID != followup.ID {
		t.Fatalf("recovered claims=%#v err=%v", claimed, err)
	}
	if err := store.CompleteInput(ctx, steering.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseInput(ctx, followup.ID); err != nil {
		t.Fatal(err)
	}
	inputs, err := store.ListInputs(ctx, "chat", 10)
	if err != nil || len(inputs) != 2 {
		t.Fatalf("inputs=%#v err=%v", inputs, err)
	}
	for _, input := range inputs {
		if input.ID == steering.ID && input.Status != InputCompleted || input.ID == followup.ID && input.Status != InputQueued {
			t.Fatalf("input status = %#v", input)
		}
	}
}

func TestTurnManagerCancelsOnlyMatchingChatBranch(t *testing.T) {
	manager := NewTurnManager()
	ctxA, finishA, err := manager.Start(context.Background(), "chat-a", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer finishA()
	ctxB, finishB, err := manager.Start(context.Background(), "chat-b", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer finishB()
	if _, _, err := manager.Start(context.Background(), "chat-a", "main"); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("duplicate turn error = %v", err)
	}
	runA, err := manager.ActiveRun("chat-a", "main")
	if err != nil || runA.ID == "" {
		t.Fatalf("active run = %#v, err=%v", runA, err)
	}
	if manager.CancelRun("chat-a", "main", "stale-run") {
		t.Fatal("stale stop cancelled an active run")
	}
	if !manager.CancelRun("chat-a", "main", runA.ID) || !manager.Active("chat-b", "main") {
		t.Fatal("cancel did not target only the requested branch")
	}
	if !errors.Is(ctxA.Err(), context.Canceled) || ctxB.Err() != nil {
		t.Fatalf("chat contexts: a=%v b=%v", ctxA.Err(), ctxB.Err())
	}
}

func structLLMResponse(text string) model.LLMResponse {
	return model.LLMResponse{Content: genai.NewContentFromText(text, genai.RoleUser)}
}
