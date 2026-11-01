package telegram

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chechiachang/abot/internal/workflow"
)

func TestActivityReporterKeepsProgressInSameMessage(t *testing.T) {
	var edits []string
	var buttons int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/editMessageText" {
			t.Errorf("Telegram method path=%q", r.URL.Path)
		}
		var request struct {
			ChatID      int64                 `json:"chat_id"`
			MessageID   int64                 `json:"message_id"`
			Text        string                `json:"text"`
			ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.ChatID != 123 || request.MessageID != 456 {
			t.Errorf("edited Telegram message = chat %d, message %d", request.ChatID, request.MessageID)
		}
		edits = append(edits, request.Text)
		if request.ReplyMarkup != nil {
			buttons++
		}
		_, _ = w.Write([]byte("{\"ok\":true,\"result\":true}"))
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	reporter := newActivityReporter(&Bridge{Client: client}, 123, 456, "run-uuid")
	reporter.branchID = "main"
	reporter.OnEvent(workflow.Event{Agent: "coordinator", Partial: true, Text: "private chain of thought"})
	reporter.OnEvent(workflow.Event{Agent: "coordinator", Tool: "coding_agent", ToolState: "started"})
	reporter.mu.Lock()
	reporter.lastEdit = reporter.lastEdit.Add(-3 * time.Second)
	reporter.mu.Unlock()
	reporter.OnEvent(workflow.Event{Agent: "coding_agent", Tool: "read", ToolState: "started"})
	reporter.OnEvent(workflow.Event{Agent: "coding_agent", Tool: "read", ToolState: "completed"})
	score := 0.696
	reporter.OnEvent(workflow.Event{Kind: workflow.EventSelection, Agent: "coordinator", Selection: &workflow.Selection{
		SystemOne: "selected", Confidence: &score, MinConfidence: 0.6, CandidateCount: 4,
		PreviousModel: "gpt-6-luna", Model: "gpt-6.1-sol", Effort: "xhigh",
	}})
	reporter.mu.Lock()
	for i := 0; i < 15; i++ {
		reporter.addProgressLocked(fmt.Sprintf("Completed tool %d", i))
	}
	reporter.mu.Unlock()
	reporter.Finish("Completed.")

	if len(edits) < 2 {
		t.Fatalf("expected in-place progress edits, got %d", len(edits))
	}
	if !strings.Contains(edits[1], "Working: <code>coding_agent</code>") || !strings.Contains(edits[1], "Delegating work to <code>coding_agent</code>") {
		t.Errorf("coding-agent progress edit = %q", edits[1])
	}
	final := edits[len(edits)-1]
	for _, want := range []string{"Completed.", "Progress:", "Coordinator:", "System One: selected", "score 0.6960", "minimum 0.6", "candidates 4", "Model using: <code>gpt-6.1-sol</code> / xhigh", "Model changed: <code>gpt-6-luna</code> / default", "Completed tool 14"} {
		if !strings.Contains(final, want) {
			t.Errorf("final progress message %q does not contain %q", final, want)
		}
	}
	if strings.Contains(final, "private chain of thought") {
		t.Fatalf("progress message exposed generated model text: %q", final)
	}
}

func TestSelectionProgressFallbackAndUnchangedModel(t *testing.T) {
	score := 0.08249701028169465
	for _, test := range []struct {
		name      string
		selection workflow.Selection
		want      []string
	}{
		{"low confidence", workflow.Selection{SystemOne: "fallback", Confidence: &score, MinConfidence: 0.6, CandidateCount: 4, HTTPStatus: 200, FallbackReason: "low_confidence"}, []string{"score 0.0825", "minimum 0.6", "low_confidence", "HTTP 200"}},
		{"HTTP failure", workflow.Selection{SystemOne: "fallback", HTTPStatus: 400, FallbackReason: "http_status"}, []string{"score unavailable", "HTTP 400"}},
		{"disabled", workflow.Selection{SystemOne: "disabled"}, []string{"System One: disabled"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := test.selection
			selection.Model, selection.PreviousModel = "gpt-6-luna", "gpt-6-luna"
			selection.Effort, selection.PreviousEffort = "medium", "medium"
			text := strings.Join(selectionProgressLines(selection), "\n")
			for _, want := range append(test.want, "Model using: `gpt-6-luna` / medium") {
				if !strings.Contains(text, want) {
					t.Errorf("progress %q missing %q", text, want)
				}
			}
			if strings.Contains(text, "changed:") {
				t.Errorf("unchanged selection reported a change: %q", text)
			}
		})
	}
}
