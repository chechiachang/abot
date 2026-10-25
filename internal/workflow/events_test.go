package workflow

import (
	"strings"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestNewRequestIDAndFromADK(t *testing.T) {
	requestID, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(requestID, "req_") || len(requestID) != 36 {
		t.Fatalf("request ID = %q", requestID)
	}
	ids := IDs{RequestID: requestID, ChatID: "chat-1"}

	progress := FromADK(ids, "coordinator", &session.Event{
		Author:      "coordinator",
		LLMResponse: structResponse(true, "thinking"),
	})
	if progress.Kind != EventProgress || !progress.Partial || progress.Text != "thinking" || progress.IDs != ids {
		t.Fatalf("progress event = %+v", progress)
	}
	result := FromADK(ids, "coordinator", &session.Event{
		Author:      "coordinator",
		LLMResponse: structResponse(false, "done"),
	})
	if result.Kind != EventResult || result.Text != "done" {
		t.Fatalf("result event = %+v", result)
	}
}

func structResponse(partial bool, text string) model.LLMResponse {
	return model.LLMResponse{Content: genai.NewContentFromText(text, genai.RoleModel), Partial: partial}
}
