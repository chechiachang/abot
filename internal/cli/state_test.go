package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentruntime "github.com/chechiachang/abot/internal/runtime"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestStateInspectorReadsSQLiteAndRedactsRawDetails(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")
	store, err := agentruntime.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Sessions().Create(context.Background(), &session.CreateRequest{AppName: "abot", UserID: "user-1", SessionID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	event := &session.Event{
		ID: "event-1", Timestamp: time.Now().UTC(), InvocationID: "generation-1", Branch: "main", Author: "coordinator",
		LLMResponse: model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "read", Response: map[string]any{"api_key": "tool-secret", "text": "hello"}}}}}},
	}
	if err := store.Sessions().AppendEvent(context.Background(), created.Session, event); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginTurn(context.Background(), "chat-1", "main", "generation-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishTurn(context.Background(), "generation-1", agentruntime.TaskCompleted, "event-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	contents := "azure:\n  base_url: https://example.test/openai/v1\n  model: model-a\nruntime:\n  sqlite_path: " + dbPath + "\n"
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZURE_OPENAI_API_KEY", "azure-secret-key")
	cmd := NewRootCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--config", configPath, "state", "events", "chat-1", "--user-id", "user-1", "--raw"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "tool-secret") || strings.Contains(output.String(), "azure-secret-key") || !strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("inspector output = %q", output.String())
	}
}
