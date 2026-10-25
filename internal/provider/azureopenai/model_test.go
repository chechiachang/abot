package azureopenai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestGenerateContentMapsMessagesToolsAndResponse(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/chat/completions" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if got := r.Header.Get("api-key"); got != "test-api-key" {
			t.Errorf("api-key header = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		received <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done","tool_calls":[{"id":"call_next","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`)
	}))
	defer server.Close()
	llm := newTestModel(t, server.URL, "test-api-key")

	request := &adkmodel.LLMRequest{
		Model: "deployment-override",
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText("system prompt", genai.RoleUser),
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name: "read",
				Parameters: &genai.Schema{Type: genai.Type("OBJECT"), Properties: map[string]*genai.Schema{
					"path": {Type: genai.Type("STRING")},
				}, Required: []string{"path"}},
			}}}},
		},
		Contents: []*genai.Content{
			genai.NewContentFromText("read the file", genai.RoleUser),
			{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "call_prev", Name: "read", Args: map[string]any{"path": "README.md"}}}}},
			{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "call_prev", Name: "read", Response: map[string]any{"output": "contents"}}}}},
		},
	}
	var result *adkmodel.LLMResponse
	for response, err := range llm.GenerateContent(context.Background(), request, false) {
		if err != nil {
			t.Fatal(err)
		}
		result = response
	}
	if result == nil || !result.TurnComplete || result.UsageMetadata == nil {
		t.Fatalf("unexpected response: %#v", result)
	}
	if got := result.Content.Parts[0].Text; got != "done" {
		t.Fatalf("response text = %q", got)
	}
	if call := result.Content.Parts[1].FunctionCall; call == nil || call.ID != "call_next" || call.Name != "read" || call.Args["path"] != "README.md" {
		t.Fatalf("tool call = %#v", result.Content.Parts[1].FunctionCall)
	}
	body := <-received
	if body["model"] != "deployment-override" {
		t.Fatalf("model = %v", body["model"])
	}
	if _, exists := body["reasoning_effort"]; exists {
		t.Fatalf("unset reasoning effort was sent: %#v", body["reasoning_effort"])
	}
	messages := body["messages"].([]any)
	if len(messages) != 4 || messages[0].(map[string]any)["role"] != "system" || messages[2].(map[string]any)["tool_calls"] == nil {
		t.Fatalf("messages were not mapped: %#v", messages)
	}
	if messages[3].(map[string]any)["tool_call_id"] != "call_prev" {
		t.Fatalf("tool response id was lost: %#v", messages[3])
	}
	tools := body["tools"].([]any)
	parameters := tools[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	if parameters["type"] != "object" {
		t.Fatalf("schema type = %v", parameters["type"])
	}
}

func TestGenerateContentStreamsTextAndToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"index":0,"delta":{"content":"hello"}}]}`)
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`)
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)
		_, _ = fmt.Fprintln(w, `data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
		_, _ = fmt.Fprintln(w, "data: [DONE]")
	}))
	defer server.Close()
	llm := newTestModel(t, server.URL, "key")
	var partials int
	var final *adkmodel.LLMResponse
	for response, err := range llm.GenerateContent(context.Background(), &adkmodel.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("go", genai.RoleUser)}}, true) {
		if err != nil {
			t.Fatal(err)
		}
		if response.Partial {
			partials++
		} else {
			final = response
		}
	}
	if partials != 1 || final == nil || !final.TurnComplete {
		t.Fatalf("partials=%d final=%#v", partials, final)
	}
	if final.Content.Parts[0].Text != "hello" || final.Content.Parts[1].FunctionCall.Args["path"] != "a.txt" {
		t.Fatalf("stream result = %#v", final.Content.Parts)
	}
	if final.UsageMetadata.TotalTokenCount != 6 {
		t.Fatalf("usage = %#v", final.UsageMetadata)
	}
}

func TestFunctionToolsRetryWithNoneWhenAzureRequiresIt(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if attempts == 1 {
			if body["stream"] != true || body["stream_options"] == nil {
				t.Errorf("first request was not streamed: %#v", body)
			}
			if _, sent := body["reasoning_effort"]; sent {
				t.Errorf("first request unexpectedly sent reasoning_effort: %#v", body)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","param":"reasoning_effort","message":"Function tools with reasoning_effort are not supported for this model. To use function tools, use /v1/responses or set reasoning_effort to 'none'."}}`)
			return
		}
		if body["stream"] != true || body["stream_options"] == nil || body["reasoning_effort"] != "none" || len(body["tools"].([]any)) != 1 {
			t.Errorf("fallback request changed incorrectly: %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `data: {"choices":[{"index":0,"delta":{"content":"fallback worked"},"finish_reason":"stop"}]}`)
		_, _ = fmt.Fprintln(w, "data: [DONE]")
	}))
	defer server.Close()
	llm := newTestModel(t, server.URL, "key")
	request := &adkmodel.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hello", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name: "read", Parameters: &genai.Schema{Type: genai.Type("OBJECT")},
		}}}}},
	}
	var result *adkmodel.LLMResponse
	for response, err := range llm.GenerateContent(context.Background(), request, true) {
		if err != nil {
			t.Fatal(err)
		}
		result = response
	}
	if attempts != 2 || result == nil || !result.TurnComplete || result.Content.Parts[0].Text != "fallback worked" {
		t.Fatalf("attempts=%d result=%#v", attempts, result)
	}
}

func TestNoneRetryRequiresToolsAndNoConfiguredEffort(t *testing.T) {
	err := &ProviderError{Kind: ErrorInvalidRequest, Param: "reasoning_effort", ReasoningEffortIssue: "none_required"}
	if shouldRetryWithNoReasoning(err, chatRequest{Tools: []functionTool{{}}}) != true {
		t.Fatal("did not retry a tool request with unset reasoning")
	}
	if shouldRetryWithNoReasoning(err, chatRequest{Tools: []functionTool{{}}, ReasoningEffort: "high"}) {
		t.Fatal("overrode explicitly configured reasoning effort")
	}
	if shouldRetryWithNoReasoning(err, chatRequest{}) {
		t.Fatal("retried a request without tools")
	}
}

func TestMakeRequestSetsConfiguredReasoningEffort(t *testing.T) {
	body, err := makeRequest(&adkmodel.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("hello", genai.RoleUser)}}, "deployment", "none", false)
	if err != nil {
		t.Fatal(err)
	}
	if body.ReasoningEffort != "none" {
		t.Fatalf("reasoning effort = %q", body.ReasoningEffort)
	}
}

func TestReasoningEffortRejectionExplainsConfiguration(t *testing.T) {
	if err := (&ProviderError{Kind: ErrorInvalidRequest, StatusCode: http.StatusBadRequest, Param: "reasoning_effort"}).Error(); !strings.Contains(err, "request omitted it") || !strings.Contains(err, "compatibility") {
		t.Fatalf("omitted-effort error is not actionable: %q", err)
	}
	err := (&ProviderError{Kind: ErrorInvalidRequest, StatusCode: http.StatusBadRequest, Param: "reasoning_effort", ReasoningEffortWasSent: true}).Error()
	if !strings.Contains(err, "AZURE_OPENAI_REASONING_EFFORT") || !strings.Contains(err, "policy thinking_level") {
		t.Fatalf("configured-effort error does not explain settings: %q", err)
	}
}

func TestClassifyReasoningEffortIssue(t *testing.T) {
	for _, test := range []struct{ code, message, want string }{
		{"unsupported_parameter", "", "unsupported"},
		{"", "set reasoning_effort to 'none'", "none_required"},
		{"", "reasoning_effort is required", "required"},
		{"", "invalid value; allowed values are low and high", "invalid_value"},
		{"", "request rejected", "unknown"},
	} {
		if got := classifyReasoningEffortIssue(test.code, test.message); got != test.want {
			t.Errorf("classifyReasoningEffortIssue(%q, %q) = %q, want %q", test.code, test.message, got, test.want)
		}
	}
}

func TestMakeRequestMapsBoundedInlineImagesToOpenAIChatContent(t *testing.T) {
	request := &adkmodel.LLMRequest{Contents: []*genai.Content{{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{genai.NewPartFromText("describe"), {InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte("png")}}},
	}}}
	body, err := makeRequest(request, "deployment", "", false)
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := body.Messages[0].Content.([]chatInputPart)
	if !ok || len(parts) != 2 || parts[0].Text != "describe" || parts[1].Type != "image_url" || !strings.HasSuffix(parts[1].ImageURL.URL, "cG5n") {
		t.Fatalf("multimodal content = %#v", body.Messages[0].Content)
	}

	request.Contents[0].Parts[1].InlineData.MIMEType = "image/svg+xml"
	if _, err := makeRequest(request, "deployment", "", false); err == nil {
		t.Fatal("expected unsupported inline image MIME type to fail")
	}
}

func TestAuthenticationErrorsAreTypedAndDoNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":{"code":"invalid_key","param":"auth","message":"test-api-key"}}`)
	}))
	defer server.Close()
	llm := newTestModel(t, server.URL, "test-api-key")
	var got error
	for _, err := range llm.GenerateContent(context.Background(), &adkmodel.LLMRequest{}, false) {
		got = err
	}
	var providerErr *ProviderError
	if !errors.As(got, &providerErr) || providerErr.Kind != ErrorAuthentication || providerErr.Retryable() {
		t.Fatalf("error = %#v", got)
	}
	if strings.Contains(got.Error(), "test-api-key") {
		t.Fatalf("error leaked credential: %v", got)
	}
	if !strings.Contains(got.Error(), "code=invalid_key") || !strings.Contains(got.Error(), "param=auth") {
		t.Fatalf("error omitted safe provider details: %v", got)
	}
}

func TestGenerateContentRetriesTransientProviderErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, `{"error":{"code":"busy"}}`, http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	llm, err := NewModel(Config{BaseURL: server.URL, APIKey: "key", Model: "deployment", MaxRetries: 1, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var result *adkmodel.LLMResponse
	for response, err := range llm.GenerateContent(context.Background(), &adkmodel.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)}}, false) {
		if err != nil {
			t.Fatal(err)
		}
		result = response
	}
	if attempts != 2 || result == nil || result.Content.Parts[0].Text != "recovered" {
		t.Fatalf("attempts=%d result=%#v", attempts, result)
	}
}

func TestCancelledRequestPropagates(t *testing.T) {
	llm, err := NewModel(Config{BaseURL: "https://example.com", APIKey: "key", Model: "model", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got error
	for _, err := range llm.GenerateContent(ctx, &adkmodel.LLMRequest{}, false) {
		got = err
	}
	if !errors.Is(got, context.Canceled) {
		t.Fatalf("error = %v", got)
	}
}

func newTestModel(t *testing.T, baseURL, apiKey string) *Model {
	t.Helper()
	llm, err := NewModel(Config{BaseURL: baseURL, APIKey: apiKey, Model: "test-deployment", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return llm
}
