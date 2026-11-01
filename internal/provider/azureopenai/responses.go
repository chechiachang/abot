package azureopenai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

type responsesRequest struct {
	Model           string            `json:"model"`
	Input           []any             `json:"input"`
	Reasoning       map[string]string `json:"reasoning"`
	Tools           []any             `json:"tools,omitempty"`
	ToolChoice      any               `json:"tool_choice,omitempty"`
	Stream          bool              `json:"stream"`
	Store           bool              `json:"store"`
	Include         []string          `json:"include"`
	MaxOutputTokens int32             `json:"max_output_tokens,omitempty"`
	Temperature     *float32          `json:"temperature,omitempty"`
	TopP            *float32          `json:"top_p,omitempty"`
}

func makeResponsesRequest(req *model.LLMRequest, chat chatRequest) (responsesRequest, error) {
	if len(chat.Stop) > 0 || chat.PresencePenalty != nil || chat.FrequencyPenalty != nil {
		return responsesRequest{}, &ProviderError{Kind: ErrorInvalidRequest}
	}
	out := responsesRequest{
		Model: chat.Model, Reasoning: map[string]string{"effort": chat.ReasoningEffort},
		Stream: chat.Stream, Include: []string{"reasoning.encrypted_content"},
		MaxOutputTokens: chat.MaxCompletionTokens, Temperature: chat.Temperature, TopP: chat.TopP,
		ToolChoice: chat.ToolChoice,
	}
	for _, tool := range chat.Tools {
		out.Tools = append(out.Tools, map[string]any{
			"type": "function", "name": tool.Function.Name, "description": tool.Function.Description,
			"parameters": tool.Function.Parameters, "strict": false,
		})
	}
	appendMessages := func(messages []chatMessage) {
		for _, message := range messages {
			if message.Role == "tool" {
				out.Input = append(out.Input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
				continue
			}
			if message.Content != nil {
				content := message.Content
				if parts, ok := content.([]chatInputPart); ok {
					converted := make([]any, 0, len(parts))
					for _, part := range parts {
						if part.Type == "text" {
							converted = append(converted, map[string]any{"type": "input_text", "text": part.Text})
						} else if part.ImageURL != nil {
							converted = append(converted, map[string]any{"type": "input_image", "image_url": part.ImageURL.URL, "detail": part.ImageURL.Detail})
						}
					}
					content = converted
				}
				out.Input = append(out.Input, map[string]any{"role": message.Role, "content": content})
			}
			for _, call := range message.ToolCalls {
				out.Input = append(out.Input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
			}
		}
	}
	if req.Config != nil && req.Config.SystemInstruction != nil {
		if text := contentText(req.Config.SystemInstruction); text != "" {
			appendMessages([]chatMessage{{Role: "system", Content: text}})
		}
	}
	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		// Preserve stateless reasoning across tool calls and durable sessions.
		for _, part := range content.Parts {
			if part != nil && part.Thought && len(part.ThoughtSignature) > 0 {
				var item responsesOutput
				if json.Unmarshal(part.ThoughtSignature, &item) == nil && item.Type == "reasoning" && item.EncryptedContent != "" {
					out.Input = append(out.Input, json.RawMessage(part.ThoughtSignature))
				}
			}
		}
		messages, err := contentMessages(content)
		if err != nil {
			return responsesRequest{}, err
		}
		appendMessages(messages)
	}
	return out, nil
}

type responsesOutput struct {
	Type             string `json:"type"`
	CallID           string `json:"call_id"`
	Name             string `json:"name"`
	Arguments        string `json:"arguments"`
	EncryptedContent string `json:"encrypted_content"`
	Content          []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

type responsesEnvelope struct {
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Usage  *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

func responseContent(envelope responsesEnvelope, modelName string) (*model.LLMResponse, error) {
	if envelope.Status != "completed" && envelope.Status != "incomplete" {
		return nil, &ProviderError{Kind: ErrorProvider}
	}
	message := chatMessage{}
	var orderedParts []*genai.Part
	var text strings.Builder
	flushText := func() {
		if text.Len() > 0 {
			orderedParts = append(orderedParts, genai.NewPartFromText(text.String()))
			text.Reset()
		}
	}
	for _, raw := range envelope.Output {
		if envelope.Status == "incomplete" && bytes.Contains(raw, []byte(`"type":"function_call"`)) {
			continue
		}
		var item responsesOutput
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, &ProviderError{Kind: ErrorProvider, Cause: err}
		}
		switch item.Type {
		case "reasoning":
			flushText()
			if item.EncryptedContent != "" {
				orderedParts = append(orderedParts, &genai.Part{Thought: true, ThoughtSignature: append([]byte(nil), raw...)})
			}
		case "function_call":
			flushText()
			if item.CallID == "" || item.Name == "" {
				return nil, &ProviderError{Kind: ErrorProvider}
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
				return nil, &ProviderError{Kind: ErrorProvider, Cause: err}
			}
			orderedParts = append(orderedParts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: item.CallID, Name: item.Name, Args: args}})
		case "message":
			for _, content := range item.Content {
				if content.Type == "output_text" {
					text.WriteString(content.Text)
				} else if content.Type == "refusal" {
					text.WriteString(content.Refusal)
				}
			}
			flushText()
		}
	}
	message.Content = text.String()
	reason := "stop"
	if envelope.Status == "incomplete" {
		reason = "length"
		if envelope.IncompleteDetails.Reason == "content_filter" {
			reason = "content_filter"
		}
		// Never dispatch a function call whose generation was interrupted.
		orderedParts = nil
	}
	var tokens *usage
	if envelope.Usage != nil {
		tokens = &usage{PromptTokens: envelope.Usage.InputTokens, CompletionTokens: envelope.Usage.OutputTokens, TotalTokens: envelope.Usage.TotalTokens}
	}
	flushText()
	if len(orderedParts) == 0 {
		result, err := completionResponse(message, reason, tokens, modelName)
		if err == nil {
			result.Content.Parts = orderedParts
		}
		return result, err
	}
	var textResult strings.Builder
	for _, part := range orderedParts {
		if part.Text != "" {
			textResult.WriteString(part.Text)
		}
	}
	message.Content = textResult.String()
	result, err := completionResponse(message, reason, tokens, modelName)
	if err == nil {
		result.Content.Parts = orderedParts
	}
	return result, err
}

func (m *Model) generateResponses(ctx context.Context, body responsesRequest, yield func(*model.LLMResponse, error) bool) {
	encoded, err := json.Marshal(body)
	if err != nil {
		yield(nil, &ProviderError{Kind: ErrorInvalidRequest, Cause: err})
		return
	}
	resp, err := m.sendRequest(ctx, "/responses", encoded, body.Stream, body.Reasoning["effort"])
	if err != nil {
		yield(nil, err)
		return
	}
	defer resp.Body.Close()
	if !body.Stream {
		var envelope responsesEnvelope
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			yield(nil, &ProviderError{Kind: ErrorProvider, Cause: err})
			return
		}
		result, err := responseContent(envelope, body.Model)
		yield(result, err)
		return
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 16*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var event struct {
			Type     string            `json:"type"`
			Delta    string            `json:"delta"`
			Response responsesEnvelope `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			yield(nil, &ProviderError{Kind: ErrorProvider, Cause: err})
			return
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" && !yield(&model.LLMResponse{Content: genai.NewContentFromText(event.Delta, genai.RoleModel), Partial: true, ModelVersion: body.Model}, nil) {
				return
			}
		case "response.completed", "response.incomplete":
			result, err := responseContent(event.Response, body.Model)
			yield(result, err)
			return
		case "response.failed", "error":
			yield(nil, &ProviderError{Kind: ErrorProvider})
			return
		}
	}
	if ctx.Err() != nil {
		yield(nil, ctx.Err())
	} else {
		yield(nil, &ProviderError{Kind: ErrorProvider, Cause: scanner.Err()})
	}
}
