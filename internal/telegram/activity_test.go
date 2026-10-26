package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chechiachang/abot/internal/workflow"
)

func TestActivityReporterKeepsProgressInSameMessage(t *testing.T) {
	var edits []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/editMessageText" {
			t.Errorf("Telegram method path=%q", r.URL.Path)
		}
		var request struct {
			ChatID    int64  `json:"chat_id"`
			MessageID int64  `json:"message_id"`
			Text      string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.ChatID != 123 || request.MessageID != 456 {
			t.Errorf("edited Telegram message = chat %d, message %d", request.ChatID, request.MessageID)
		}
		edits = append(edits, request.Text)
		_, _ = w.Write([]byte("{\"ok\":true,\"result\":true}"))
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	reporter := newActivityReporter(&Bridge{Client: client}, 123, 456)
	reporter.OnEvent(workflow.Event{Agent: "coordinator", Partial: true, Text: "private chain of thought"})
	reporter.OnEvent(workflow.Event{Agent: "coordinator", Tool: "coding_agent", ToolState: "started"})
	reporter.mu.Lock()
	reporter.lastEdit = reporter.lastEdit.Add(-3 * time.Second)
	reporter.mu.Unlock()
	reporter.OnEvent(workflow.Event{Agent: "coding_agent", Tool: "read", ToolState: "started"})
	reporter.OnEvent(workflow.Event{Agent: "coding_agent", Tool: "read", ToolState: "completed"})
	reporter.Finish("Completed.")

	if len(edits) < 2 {
		t.Fatalf("expected in-place progress edits, got %d", len(edits))
	}
	if !strings.Contains(edits[1], "Working: <code>coding_agent</code>") || !strings.Contains(edits[1], "Delegating work to <code>coding_agent</code>") {
		t.Errorf("coding-agent progress edit = %q", edits[1])
	}
	final := edits[len(edits)-1]
	for _, want := range []string{"Completed.", "Progress:", "<code>coding_agent</code>", "<code>read</code>", "completed"} {
		if !strings.Contains(final, want) {
			t.Errorf("final progress message %q does not contain %q", final, want)
		}
	}
	if strings.Contains(final, "private chain of thought") {
		t.Fatalf("progress message exposed generated model text: %q", final)
	}
}
