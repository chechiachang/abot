package azureopenai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

type Config struct {
	BaseURL         string
	APIKey          string
	Model           string
	ReasoningEffort string
	MaxRetries      int
	Timeout         time.Duration
}

type Model struct {
	baseURL         string
	apiKey          string
	model           string
	reasoningEffort string
	maxRetries      int
	client          *http.Client
}

type ErrorKind string

const (
	ErrorInvalidRequest ErrorKind = "invalid_request"
	ErrorAuthentication ErrorKind = "authentication"
	ErrorRateLimit      ErrorKind = "rate_limit"
	ErrorProvider       ErrorKind = "provider"
)

type ProviderError struct {
	Kind                   ErrorKind
	StatusCode             int
	Code                   string
	Param                  string
	ReasoningEffortWasSent bool
	ReasoningEffortIssue   string
	Cause                  error
}

func (e *ProviderError) Error() string {
	if e.Kind == ErrorInvalidRequest && e.Param == "reasoning_effort" {
		code := safeErrorToken(e.Code)
		issue := e.ReasoningEffortIssue
		if issue == "" {
			issue = "unknown"
		}
		if !e.ReasoningEffortWasSent {
			details := ""
			if code != "" {
				details = ", code=" + code
			}
			if issue == "none_required" {
				return fmt.Sprintf("azure OpenAI requires reasoning_effort=none for function tools (HTTP %d%s)", e.StatusCode, details)
			}
			return fmt.Sprintf("azure OpenAI reported reasoning_effort although this request omitted it (HTTP %d, provider_issue=%s%s); check deployment/model API compatibility", e.StatusCode, issue, details)
		}
		details := ""
		if code != "" {
			details = ", code=" + code
		}
		return fmt.Sprintf("azure OpenAI rejected configured reasoning_effort (HTTP %d, provider_issue=%s%s); check deployment support and AZURE_OPENAI_REASONING_EFFORT / azure.reasoning_effort / policy thinking_level", e.StatusCode, issue, details)
	}
	details := ""
	if code := safeErrorToken(e.Code); code != "" {
		details += ", code=" + code
	}
	if param := safeErrorToken(e.Param); param != "" {
		details += ", param=" + param
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("azure OpenAI %s error (HTTP %d%s)", e.Kind, e.StatusCode, details)
	}
	return fmt.Sprintf("azure OpenAI %s error", e.Kind)
}

func (e *ProviderError) Unwrap() error { return e.Cause }

func (e *ProviderError) Retryable() bool {
	return e.Kind == ErrorRateLimit || (e.Kind == ErrorProvider && (e.StatusCode == 0 || e.StatusCode >= 500))
}

func NewModel(cfg Config) (*Model, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	apiKey, modelName := strings.TrimSpace(cfg.APIKey), strings.TrimSpace(cfg.Model)
	if baseURL == "" || apiKey == "" || modelName == "" {
		return nil, errors.New("azure base URL, API key, and model are required")
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("azure base URL is invalid")
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		u.Path = "/openai/v1"
	} else if path != "/openai/v1" {
		return nil, errors.New("azure base URL must end in /openai/v1")
	}
	baseURL = strings.TrimRight(u.String(), "/")
	if cfg.Timeout <= 0 {
		return nil, errors.New("azure request timeout must be positive")
	}
	if cfg.MaxRetries < 0 || cfg.MaxRetries > 5 {
		return nil, errors.New("azure max retries must be between 0 and 5")
	}
	switch cfg.ReasoningEffort {
	case "", "none", "low", "medium", "high":
	default:
		return nil, errors.New("azure reasoning effort must be none, low, medium, or high")
	}
	return &Model{
		baseURL:         baseURL,
		apiKey:          cfg.APIKey,
		model:           modelName,
		reasoningEffort: cfg.ReasoningEffort,
		maxRetries:      cfg.MaxRetries,
		client:          &http.Client{Timeout: cfg.Timeout},
	}, nil
}

func (m *Model) Name() string { return m.model }

func (m *Model) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		ctx, span := otel.Tracer("abot/provider/azure-openai").Start(ctx, "model.generate", trace.WithAttributes(
			attribute.String("gen_ai.system", "azure_openai"), attribute.String("gen_ai.request.model", m.model), attribute.Bool("gen_ai.request.stream", stream),
		))
		defer span.End()
		observedYield := func(response *model.LLMResponse, err error) bool {
			if err != nil {
				span.SetStatus(codes.Error, "model request failed")
				span.SetAttributes(attribute.String("error.type", fmt.Sprintf("%T", err)))
			} else if response != nil && response.UsageMetadata != nil {
				span.SetAttributes(attribute.Int("gen_ai.usage.input_tokens", int(response.UsageMetadata.PromptTokenCount)), attribute.Int("gen_ai.usage.output_tokens", int(response.UsageMetadata.CandidatesTokenCount)))
			}
			return yield(response, err)
		}
		body, err := makeRequest(req, m.model, m.reasoningEffort, stream)
		if err != nil {
			observedYield(nil, err)
			return
		}
		if stream {
			m.generateStream(ctx, body, observedYield)
			return
		}
		resp, err := m.generate(ctx, body)
		observedYield(resp, err)
	}
}

func (m *Model) generate(ctx context.Context, body chatRequest) (*model.LLMResponse, error) {
	resp, err := m.send(ctx, body)
	if err != nil {
		if shouldRetryWithNoReasoning(err, body) {
			fallback := body
			fallback.ReasoningEffort = "none"
			trace.SpanFromContext(ctx).AddEvent("provider.reasoning_effort_none_fallback")
			return m.generate(ctx, fallback)
		}
		return nil, err
	}
	defer resp.Body.Close()
	var completion chatCompletion
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return nil, &ProviderError{Kind: ErrorProvider, Cause: err}
	}
	if len(completion.Choices) == 0 {
		return nil, &ProviderError{Kind: ErrorProvider}
	}
	return completionResponse(completion.Choices[0].Message, completion.Choices[0].FinishReason, completion.Usage, body.Model)
}

func (m *Model) generateStream(ctx context.Context, body chatRequest, yield func(*model.LLMResponse, error) bool) {
	resp, err := m.send(ctx, body)
	if err != nil {
		if shouldRetryWithNoReasoning(err, body) {
			fallback := body
			fallback.ReasoningEffort = "none"
			trace.SpanFromContext(ctx).AddEvent("provider.reasoning_effort_none_fallback")
			m.generateStream(ctx, fallback, yield)
			return
		}
		yield(nil, err)
		return
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 16*1024), 8*1024*1024)
	var text strings.Builder
	toolCalls := map[int]*toolCall{}
	var finishReason string
	var usage *usage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			yield(nil, &ProviderError{Kind: ErrorProvider, Cause: err})
			return
		}
		if chunk.Error != nil {
			yield(nil, &ProviderError{Kind: ErrorProvider})
			return
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != nil {
			finishReason = *choice.FinishReason
		}
		if choice.Delta.Content != "" {
			text.WriteString(choice.Delta.Content)
			partial := &model.LLMResponse{
				Content:      genai.NewContentFromText(choice.Delta.Content, genai.RoleModel),
				Partial:      true,
				ModelVersion: m.model,
			}
			if !yield(partial, nil) {
				return
			}
		}
		for _, delta := range choice.Delta.ToolCalls {
			call := toolCalls[delta.Index]
			if call == nil {
				call = &toolCall{Type: "function"}
				toolCalls[delta.Index] = call
			}
			if delta.ID != "" {
				call.ID = delta.ID
			}
			if delta.Type != "" {
				call.Type = delta.Type
			}
			call.Function.Name += delta.Function.Name
			call.Function.Arguments += delta.Function.Arguments
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			yield(nil, ctx.Err())
			return
		}
		yield(nil, &ProviderError{Kind: ErrorProvider, Cause: err})
		return
	}
	final, err := completionResponse(chatMessage{Content: text.String(), ToolCalls: orderedToolCalls(toolCalls)}, finishReason, usage, body.Model)
	if err != nil {
		yield(nil, err)
		return
	}
	final.TurnComplete = true
	yield(final, nil)
}

func shouldRetryWithNoReasoning(err error, body chatRequest) bool {
	if body.ReasoningEffort != "" || len(body.Tools) == 0 {
		return false
	}
	var providerErr *ProviderError
	return errors.As(err, &providerErr) && providerErr.Kind == ErrorInvalidRequest && providerErr.Param == "reasoning_effort" && !providerErr.ReasoningEffortWasSent && providerErr.ReasoningEffortIssue == "none_required"
}

func (m *Model) send(ctx context.Context, body chatRequest) (*http.Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, &ProviderError{Kind: ErrorInvalidRequest, Cause: err}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(encoded))
		if err != nil {
			return nil, &ProviderError{Kind: ErrorInvalidRequest, Cause: err}
		}
		req.Header.Set("api-key", m.apiKey)
		req.Header.Set("Content-Type", "application/json")
		if body.Stream {
			req.Header.Set("Accept", "text/event-stream")
		}
		resp, err := m.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var networkErr net.Error
			if attempt < m.maxRetries && errors.As(err, &networkErr) {
				trace.SpanFromContext(ctx).AddEvent("provider.retry", trace.WithAttributes(attribute.Int("retry.attempt", attempt+1), attribute.String("retry.kind", "network")))
				if err := waitRetry(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return nil, &ProviderError{Kind: ErrorProvider, Cause: err}
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		if attempt < m.maxRetries && retryableStatus(resp.StatusCode) {
			trace.SpanFromContext(ctx).AddEvent("provider.retry", trace.WithAttributes(attribute.Int("retry.attempt", attempt+1), attribute.Int("http.status_code", resp.StatusCode)))
			resp.Body.Close()
			if err := waitRetry(ctx, attempt); err != nil {
				return nil, err
			}
			continue
		}
		var detail struct {
			Error struct {
				Code    string `json:"code"`
				Param   string `json:"param"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&detail)
		resp.Body.Close()
		kind := ErrorProvider
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			kind = ErrorAuthentication
		} else if resp.StatusCode == http.StatusTooManyRequests {
			kind = ErrorRateLimit
		} else if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			kind = ErrorInvalidRequest
		}
		return nil, &ProviderError{
			Kind: kind, StatusCode: resp.StatusCode, Code: detail.Error.Code, Param: detail.Error.Param,
			ReasoningEffortWasSent: body.ReasoningEffort != "",
			ReasoningEffortIssue:   classifyReasoningEffortIssue(detail.Error.Code, detail.Error.Message),
		}
	}
}

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500 && code <= 599
}

func classifyReasoningEffortIssue(code, message string) string {
	detail := strings.ToLower(code + " " + message)
	switch {
	case strings.Contains(detail, "set reasoning_effort to 'none'"), strings.Contains(detail, "set reasoning_effort to none"):
		return "none_required"
	case strings.Contains(detail, "unsupported"), strings.Contains(detail, "not supported"), strings.Contains(detail, "unknown parameter"), strings.Contains(detail, "unrecognized parameter"):
		return "unsupported"
	case strings.Contains(detail, "required"), strings.Contains(detail, "missing parameter"), strings.Contains(detail, "must be provided"):
		return "required"
	case strings.Contains(detail, "invalid value"), strings.Contains(detail, "valid values"), strings.Contains(detail, "allowed values"):
		return "invalid_value"
	default:
		return "unknown"
	}
}

func waitRetry(ctx context.Context, attempt int) error {
	delay := 100 * time.Millisecond * time.Duration(1<<attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func safeErrorToken(value string) string {
	if len(value) == 0 || len(value) > 64 {
		return ""
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return ""
		}
	}
	return value
}

func makeRequest(req *model.LLMRequest, defaultModel, reasoningEffort string, stream bool) (chatRequest, error) {
	if req == nil {
		return chatRequest{}, &ProviderError{Kind: ErrorInvalidRequest}
	}
	modelName := req.Model
	if modelName == "" {
		modelName = defaultModel
	}
	if modelName == "" {
		return chatRequest{}, &ProviderError{Kind: ErrorInvalidRequest}
	}
	out := chatRequest{Model: modelName, Stream: stream, ReasoningEffort: reasoningEffort}
	if stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if req.Config != nil {
		out.Temperature = req.Config.Temperature
		out.TopP = req.Config.TopP
		out.PresencePenalty = req.Config.PresencePenalty
		out.FrequencyPenalty = req.Config.FrequencyPenalty
		out.Stop = req.Config.StopSequences
		if req.Config.MaxOutputTokens > 0 {
			out.MaxCompletionTokens = req.Config.MaxOutputTokens
		}
		if req.Config.SystemInstruction != nil {
			text := contentText(req.Config.SystemInstruction)
			if text != "" {
				out.Messages = append(out.Messages, chatMessage{Role: "system", Content: text})
			}
		}
		if err := addTools(&out, req.Config); err != nil {
			return chatRequest{}, err
		}
	}
	for _, content := range req.Contents {
		messages, err := contentMessages(content)
		if err != nil {
			return chatRequest{}, err
		}
		out.Messages = append(out.Messages, messages...)
	}
	return out, nil
}

func addTools(req *chatRequest, cfg *genai.GenerateContentConfig) error {
	for _, group := range cfg.Tools {
		for _, declaration := range group.FunctionDeclarations {
			if declaration == nil || declaration.Name == "" {
				continue
			}
			parameters := declaration.ParametersJsonSchema
			if parameters == nil && declaration.Parameters != nil {
				parameters = schemaValue(declaration.Parameters)
			}
			if parameters == nil {
				parameters = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			req.Tools = append(req.Tools, functionTool{
				Type: "function",
				Function: functionDefinition{
					Name: declaration.Name, Description: declaration.Description, Parameters: parameters,
				},
			})
		}
	}
	if len(req.Tools) == 0 || cfg.ToolConfig == nil || cfg.ToolConfig.FunctionCallingConfig == nil {
		return nil
	}
	fc := cfg.ToolConfig.FunctionCallingConfig
	if len(fc.AllowedFunctionNames) > 0 {
		allowed := make(map[string]bool, len(fc.AllowedFunctionNames))
		for _, name := range fc.AllowedFunctionNames {
			allowed[name] = true
		}
		filtered := req.Tools[:0]
		for _, tool := range req.Tools {
			if allowed[tool.Function.Name] {
				filtered = append(filtered, tool)
			}
		}
		req.Tools = filtered
	}
	if fc.Mode == genai.FunctionCallingConfigModeAny && len(req.Tools) == 0 {
		return &ProviderError{Kind: ErrorInvalidRequest, Cause: errors.New("required function set is empty")}
	}
	switch fc.Mode {
	case genai.FunctionCallingConfigModeNone:
		req.ToolChoice = "none"
	case genai.FunctionCallingConfigModeAny:
		req.ToolChoice = "required"
	case genai.FunctionCallingConfigModeValidated:
		req.ToolChoice = "auto"
	default:
		req.ToolChoice = "auto"
	}
	return nil
}

func schemaValue(schema *genai.Schema) map[string]any {
	result := map[string]any{}
	if schema.Type != "" {
		result["type"] = strings.ToLower(string(schema.Type))
	}
	if schema.Description != "" {
		result["description"] = schema.Description
	}
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}
	if schema.Format != "" {
		result["format"] = schema.Format
	}
	if schema.Nullable != nil {
		result["nullable"] = *schema.Nullable
	}
	if schema.Default != nil {
		result["default"] = schema.Default
	}
	if schema.Minimum != nil {
		result["minimum"] = *schema.Minimum
	}
	if schema.Maximum != nil {
		result["maximum"] = *schema.Maximum
	}
	if schema.MinLength != nil {
		result["minLength"] = *schema.MinLength
	}
	if schema.MaxLength != nil {
		result["maxLength"] = *schema.MaxLength
	}
	if schema.MinItems != nil {
		result["minItems"] = *schema.MinItems
	}
	if schema.MaxItems != nil {
		result["maxItems"] = *schema.MaxItems
	}
	if len(schema.AnyOf) > 0 {
		anyOf := make([]any, 0, len(schema.AnyOf))
		for _, choice := range schema.AnyOf {
			anyOf = append(anyOf, schemaValue(choice))
		}
		result["anyOf"] = anyOf
	}
	if schema.Items != nil {
		result["items"] = schemaValue(schema.Items)
	}
	if len(schema.Properties) > 0 {
		properties := make(map[string]any, len(schema.Properties))
		for name, property := range schema.Properties {
			properties[name] = schemaValue(property)
		}
		result["properties"] = properties
	}
	return result
}

func contentMessages(content *genai.Content) ([]chatMessage, error) {
	if content == nil {
		return nil, nil
	}
	role := "user"
	if content.Role == genai.RoleModel {
		role = "assistant"
	}
	message := chatMessage{Role: role}
	var tools []chatMessage
	var parts []chatInputPart
	hasImage := false
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		switch {
		case part.Text != "":
			parts = appendTextPart(parts, part.Text)
		case part.FunctionCall != nil:
			argsValue := part.FunctionCall.Args
			if argsValue == nil {
				argsValue = map[string]any{}
			}
			args, err := json.Marshal(argsValue)
			if err != nil {
				return nil, &ProviderError{Kind: ErrorInvalidRequest, Cause: err}
			}
			id := part.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("call_%s", part.FunctionCall.Name)
			}
			message.ToolCalls = append(message.ToolCalls, toolCall{
				ID: id, Type: "function",
				Function: toolFunction{Name: part.FunctionCall.Name, Arguments: string(args)},
			})
		case part.FunctionResponse != nil:
			response, err := json.Marshal(part.FunctionResponse.Response)
			if err != nil {
				return nil, &ProviderError{Kind: ErrorInvalidRequest, Cause: err}
			}
			tools = append(tools, chatMessage{
				Role: "tool", Name: part.FunctionResponse.Name, ToolCallID: part.FunctionResponse.ID, Content: string(response),
			})
		case part.InlineData != nil:
			mimeType := strings.ToLower(strings.TrimSpace(part.InlineData.MIMEType))
			if mimeType != "image/jpeg" && mimeType != "image/png" && mimeType != "image/webp" {
				return nil, &ProviderError{Kind: ErrorInvalidRequest, Cause: errors.New("only JPEG, PNG, and WebP inline images are supported")}
			}
			if len(part.InlineData.Data) == 0 || len(part.InlineData.Data) > 10<<20 {
				return nil, &ProviderError{Kind: ErrorInvalidRequest, Cause: errors.New("inline image is empty or exceeds the 10 MiB limit")}
			}
			parts = append(parts, chatInputPart{Type: "image_url", ImageURL: &imageURL{URL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(part.InlineData.Data), Detail: "auto"}})
			hasImage = true
		case part.FileData != nil:
			return nil, &ProviderError{Kind: ErrorInvalidRequest, Cause: errors.New("remote file references are not supported")}
		}
	}
	if len(parts) > 0 && hasImage {
		message.Content = parts
	} else if len(parts) > 0 {
		var text strings.Builder
		for _, part := range parts {
			if part.Type == "text" {
				if text.Len() > 0 {
					text.WriteByte('\n')
				}
				text.WriteString(part.Text)
			}
		}
		message.Content = text.String()
	}
	var messages []chatMessage
	if len(parts) > 0 || len(message.ToolCalls) > 0 {
		messages = append(messages, message)
	}
	return append(messages, tools...), nil
}

func contentText(content *genai.Content) string {
	var out strings.Builder
	for _, part := range content.Parts {
		if part != nil && part.Text != "" {
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(part.Text)
		}
	}
	return out.String()
}

type chatInputPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func appendTextPart(parts []chatInputPart, text string) []chatInputPart {
	if n := len(parts); n > 0 && parts[n-1].Type == "text" {
		parts[n-1].Text += "\n" + text
		return parts
	}
	return append(parts, chatInputPart{Type: "text", Text: text})
}

func completionResponse(message chatMessage, reason string, usage *usage, modelName string) (*model.LLMResponse, error) {
	content := &genai.Content{Role: genai.RoleModel}
	if text, ok := message.Content.(string); ok && text != "" {
		content.Parts = append(content.Parts, genai.NewPartFromText(text))
	}
	for _, call := range message.ToolCalls {
		if call.Function.Arguments == "" || call.Function.Arguments == "null" {
			call.Function.Arguments = "{}"
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return nil, &ProviderError{Kind: ErrorProvider, Cause: err}
		}
		content.Parts = append(content.Parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: call.ID, Name: call.Function.Name, Args: args}})
	}
	result := &model.LLMResponse{Content: content, ModelVersion: modelName, TurnComplete: true, FinishReason: finishReason(reason)}
	if usage != nil {
		result.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: int32(usage.PromptTokens), CandidatesTokenCount: int32(usage.CompletionTokens), TotalTokenCount: int32(usage.TotalTokens),
		}
	}
	return result, nil
}

func finishReason(reason string) genai.FinishReason {
	switch reason {
	case "stop", "tool_calls", "function_call":
		return genai.FinishReasonStop
	case "length":
		return genai.FinishReasonMaxTokens
	case "content_filter":
		return genai.FinishReasonSafety
	default:
		return genai.FinishReasonOther
	}
}

func orderedToolCalls(calls map[int]*toolCall) []toolCall {
	indices := make([]int, 0, len(calls))
	for index := range calls {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	result := make([]toolCall, 0, len(indices))
	for _, index := range indices {
		result = append(result, *calls[index])
	}
	return result
}

type chatRequest struct {
	Model               string         `json:"model"`
	ReasoningEffort     string         `json:"reasoning_effort,omitempty"`
	Messages            []chatMessage  `json:"messages"`
	Stream              bool           `json:"stream,omitempty"`
	StreamOptions       *streamOptions `json:"stream_options,omitempty"`
	MaxCompletionTokens int32          `json:"max_completion_tokens,omitempty"`
	Temperature         *float32       `json:"temperature,omitempty"`
	TopP                *float32       `json:"top_p,omitempty"`
	PresencePenalty     *float32       `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float32       `json:"frequency_penalty,omitempty"`
	Stop                []string       `json:"stop,omitempty"`
	Tools               []functionTool `json:"tools,omitempty"`
	ToolChoice          any            `json:"tool_choice,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}
type functionTool struct {
	Type     string             `json:"type"`
	Function functionDefinition `json:"function"`
}
type functionDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}
type toolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function toolFunction `json:"function"`
}
type toolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}
type chatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
}

type chatCompletion struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *usage `json:"usage,omitempty"`
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content   string          `json:"content,omitempty"`
			ToolCalls []toolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type toolCallDelta struct {
	Index    int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function toolFunction `json:"function"`
}
type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
