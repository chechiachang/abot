package azureopenai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

func reasoningToolRequest() *adkmodel.LLMRequest {
	return &adkmodel.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("Read the file.", genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "read", ParametersJsonSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}}}}},
	}
}

func TestResponsesPreservesReasoningAndToolResultsWithoutServerState(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/openai/v1/responses" || r.Header.Get("api-key") != "test-key" {
			t.Errorf("wrong Responses endpoint or authentication")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["store"] != false || body["reasoning"].(map[string]any)["effort"] != "xhigh" || body["include"].([]any)[0] != "reasoning.encrypted_content" {
			t.Errorf("incorrect reasoning/state settings: %#v", body)
		}
		tool := body["tools"].([]any)[0].(map[string]any)
		if tool["name"] != "read" || tool["strict"] != false || tool["function"] != nil {
			t.Errorf("invalid Responses tool definition: %#v", tool)
		}
		if calls == 1 {
			input := body["input"].([]any)
			if input[0].(map[string]any)["role"] != "system" {
				t.Error("system instructions were not preserved")
			}
			parts := input[1].(map[string]any)["content"].([]any)
			image := parts[1].(map[string]any)
			if image["type"] != "input_image" || !strings.HasPrefix(image["image_url"].(string), "data:image/png;base64,") {
				t.Error("inline image was not translated to Responses input")
			}
			_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"encrypted-test-value"},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"README.md\"}"}],"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`)
			return
		}
		input := body["input"].([]any)
		seenReasoning, seenCall, seenResult := false, false, false
		for _, entry := range input {
			item := entry.(map[string]any)
			switch item["type"] {
			case "reasoning":
				seenReasoning = item["encrypted_content"] == "encrypted-test-value"
			case "function_call":
				seenCall = item["call_id"] == "call_1" && item["name"] == "read"
			case "function_call_output":
				seenResult = item["call_id"] == "call_1" && strings.Contains(item["output"].(string), "file content")
			}
		}
		if !seenReasoning || !seenCall || !seenResult {
			t.Errorf("tool continuation lost reasoning or call IDs: %#v", input)
		}
		_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Done."}]}]}`)
	}))
	defer server.Close()
	llm, err := NewModel(Config{BaseURL: server.URL, APIKey: "test-key", Model: "test-model", ReasoningEffort: "xhigh", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	req := reasoningToolRequest()
	req.Config.SystemInstruction = genai.NewContentFromText("Follow the user request.", genai.RoleUser)
	req.Contents[0].Parts = append(req.Contents[0].Parts, &genai.Part{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte{1, 2, 3}}})
	var response *adkmodel.LLMResponse
	for value, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		response = value
	}
	if response == nil || len(response.Content.Parts) != 2 || !response.Content.Parts[0].Thought || response.Content.Parts[1].FunctionCall.ID != "call_1" || response.UsageMetadata.TotalTokenCount != 30 {
		t.Fatalf("invalid Responses tool output: %#v", response)
	}
	persisted, err := json.Marshal(response.Content)
	if err != nil {
		t.Fatal(err)
	}
	var restored genai.Content
	if err := json.Unmarshal(persisted, &restored); err != nil {
		t.Fatal(err)
	}
	req.Contents = append(req.Contents, &restored, &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "call_1", Name: "read", Response: map[string]any{"text": "file content"}}}}})
	for value, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		if contentText(value.Content) != "Done." {
			t.Errorf("unexpected final response")
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestResponsesStreamingRequiresTerminalEvent(t *testing.T) {
	for _, test := range []struct {
		name, terminal string
		wantError      bool
	}{
		{"completed", `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`, false},
		{"interrupted", "", true},
		{"failed", `data: {"type":"response.failed","response":{"status":"failed"}}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/openai/v1/responses" {
					t.Errorf("wrong API path: %s", r.URL.Path)
				}
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n"+test.terminal+"\n\n")
			}))
			defer server.Close()
			llm, err := NewModel(Config{BaseURL: server.URL, APIKey: "key", Model: "test-model", ReasoningEffort: "medium", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			partials, finals := 0, 0
			failed := false
			for value, err := range llm.GenerateContent(context.Background(), reasoningToolRequest(), true) {
				if err != nil {
					failed = true
					continue
				}
				if value.Partial {
					partials++
				} else {
					finals++
				}
			}
			if failed != test.wantError || partials != 1 || (!test.wantError && finals != 1) || (test.wantError && finals != 0) {
				t.Fatalf("partial=%d final=%d failed=%v", partials, finals, failed)
			}
		})
	}
}

func TestIncompleteResponsesDoNotDispatchFunctionCalls(t *testing.T) {
	envelope := responsesEnvelope{Status: "incomplete", Output: []json.RawMessage{json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"write","arguments":"{"}`)}}
	envelope.IncompleteDetails.Reason = "max_output_tokens"
	response, err := responseContent(envelope, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if response.FinishReason != genai.FinishReasonMaxTokens || len(response.Content.Parts) != 0 {
		t.Fatalf("incomplete tool call must not be dispatched: %#v", response)
	}
}

func TestResponseContentPreservesInterleavedOutputOrder(t *testing.T) {
	envelope := responsesEnvelope{Status: "completed", Output: []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","encrypted_content":"rs_1"}`),
		json.RawMessage(`{"type":"message","content":[{"type":"output_text","text":"before"}]}`),
		json.RawMessage(`{"type":"reasoning","encrypted_content":"rs_2"}`),
		json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"README.md\"}"}`),
		json.RawMessage(`{"type":"message","content":[{"type":"output_text","text":"after"}]}`),
	}}
	response, err := responseContent(envelope, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	parts := response.Content.Parts
	if len(parts) != 5 || !parts[0].Thought || parts[1].Text != "before" || !parts[2].Thought || parts[3].FunctionCall == nil || parts[3].FunctionCall.ID != "call_1" || parts[4].Text != "after" {
		t.Fatalf("output ordering changed: %#v", parts)
	}
}
