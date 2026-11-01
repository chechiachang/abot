package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	routingTimeout       = 2 * time.Second
	routingMaxPrompt     = 4096
	routingMaxBody       = 8192
	routingMaxCandidates = 32
)

type routingCandidate struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

var routingHTTPClient = &http.Client{
	Timeout: routingTimeout,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type routingChoice struct {
	Model      string   `json:"model"`
	Effort     string   `json:"effort"`
	Confidence *float64 `json:"confidence"`
}

type routingRequest struct {
	Model      string             `json:"model"`
	Prompt     string             `json:"prompt"`
	Candidates []routingCandidate `json:"candidates"`
}

func routeDecision(ctx context.Context, client *http.Client, endpoint, apiKey, model, prompt string, candidates []routingCandidate, fallback routingChoice, minConfidence float64) (routingChoice, bool) {
	if client == nil || len(prompt) == 0 || len(prompt) > routingMaxPrompt || len(candidates) == 0 || len(candidates) > routingMaxCandidates {
		return fallback, false
	}
	choices := make([]routingCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Model == "" {
			return fallback, false
		}
		if !slices.Contains(choices, candidate) {
			choices = append(choices, candidate)
		}
	}
	if !slices.Contains(choices, routingCandidate{Model: fallback.Model, Effort: fallback.Effort}) {
		return fallback, false
	}
	body, err := json.Marshal(routingRequest{Model: model, Prompt: prompt, Candidates: choices})
	if err != nil {
		return fallback, false
	}
	callCtx, cancel := context.WithTimeout(ctx, routingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fallback, false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fallback, false
	}
	defer resp.Body.Close()
	limited, err := io.ReadAll(io.LimitReader(resp.Body, routingMaxBody+1))
	if err != nil || len(limited) > routingMaxBody || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fallback, false
	}
	var choice routingChoice
	decoder := json.NewDecoder(bytes.NewReader(limited))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&choice); err != nil {
		return fallback, false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fallback, false
	}
	if choice.Confidence == nil || *choice.Confidence < minConfidence || *choice.Confidence > 1 || strings.TrimSpace(choice.Model) != choice.Model || strings.TrimSpace(choice.Effort) != choice.Effort || !containsRouteChoice(choices, choice) {
		return fallback, false
	}
	return routingChoice{Model: choice.Model, Effort: choice.Effort}, true
}

func containsRouteChoice(choices []routingCandidate, choice routingChoice) bool {
	for _, candidate := range choices {
		if candidate.Model == choice.Model && candidate.Effort == choice.Effort {
			return true
		}
	}
	return false
}
