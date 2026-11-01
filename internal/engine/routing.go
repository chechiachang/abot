package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
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
	Model      string
	Effort     string
	Confidence *float64
}

// System One protocol request and response envelopes.
type systemOneRequest struct {
	Model     string                       `json:"model"`
	State     string                       `json:"state"`
	Questions map[string]systemOneQuestion `json:"questions"`
}

type systemOneQuestion struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
}

type systemOneResponse struct {
	Answers map[string]struct {
		Choice     json.RawMessage `json:"choice"`
		Confidence json.RawMessage `json:"confidence"`
	} `json:"answers"`
}

func routeDecision(ctx context.Context, client *http.Client, endpoint, apiKey, model, prompt string, candidates []routingCandidate, fallback routingChoice, minConfidence float64) (routingChoice, bool) {
	if client == nil || !isHTTPSURL(endpoint) || strings.TrimSpace(apiKey) == "" || strings.TrimSpace(model) == "" || minConfidence < 0 || minConfidence > 1 || math.IsNaN(minConfidence) || len(prompt) == 0 || len(prompt) > routingMaxPrompt || len(candidates) == 0 || len(candidates) > routingMaxCandidates {
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
	keys := make(map[string]routingCandidate, len(choices))
	options := make([]string, 0, len(choices))
	for i, candidate := range choices {
		key := "candidate_" + strconv.Itoa(i)
		keys[key] = candidate
		options = append(options, key+": model="+candidate.Model+", effort="+candidate.Effort)
	}
	requestBody := systemOneRequest{
		Model: model,
		State: prompt,
		Questions: map[string]systemOneQuestion{
			"selection": {Question: "Choose exactly one allowed model and effort pair for this task. Options: " + strings.Join(options, "; "), Choices: options},
		},
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return fallback, false
	}
	callCtx, cancel := context.WithTimeout(ctx, routingTimeout)
	defer cancel()
	u, _ := url.Parse(endpoint)
	u.Path = strings.TrimRight(u.Path, "/") + "/providers/microsoft/v1/systemone"
	u.RawPath = ""
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fallback, false
	}
	req.Header.Set("api-key", apiKey)
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
	var parsed systemOneResponse
	decoder := json.NewDecoder(bytes.NewReader(limited))
	if err := decoder.Decode(&parsed); err != nil {
		return fallback, false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fallback, false
	}
	answer, ok := parsed.Answers["selection"]
	if !ok || len(answer.Choice) == 0 || string(answer.Choice) == "null" {
		return fallback, false
	}
	var key string
	if err := json.Unmarshal(answer.Choice, &key); err != nil {
		var choiceObj struct {
			Key    string `json:"key"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
		}
		if json.Unmarshal(answer.Choice, &choiceObj) != nil {
			return fallback, false
		}
		for candidateKey, candidate := range keys {
			if (choiceObj.Key != "" && candidateKey == choiceObj.Key) || (choiceObj.Model != "" && candidate.Model == choiceObj.Model && candidate.Effort == choiceObj.Effort) {
				key = candidateKey
				break
			}
		}
	}
	choice, ok := keys[key]
	confidence, okConfidence := parseConfidence(answer.Confidence)
	if !ok || !okConfidence || confidence < minConfidence || confidence > 1 {
		return fallback, false
	}
	return routingChoice{Model: choice.Model, Effort: choice.Effort, Confidence: &confidence}, true
}

func parseConfidence(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
		return value, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		parsed, err := strconv.ParseFloat(text, 64)
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	}
	return 0, false
}

func isHTTPSURL(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func containsRouteChoice(choices []routingCandidate, choice routingChoice) bool {
	return slices.Contains(choices, routingCandidate{Model: choice.Model, Effort: choice.Effort})
}
