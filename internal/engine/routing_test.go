package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/policy"
)

func TestRouteDecisionUsesMicrosoftSystemOneProtocolAndParsesAnswer(t *testing.T) {
	allowed := []routingCandidate{{Model: "safe", Effort: "low"}, {Model: "strong", Effort: "high"}}
	fallback := routingChoice{Model: "safe", Effort: "low"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/base/providers/microsoft/v1/systemone" {
			t.Errorf("path=%q", r.URL.Path)
		}
		if r.Header.Get("api-key") != "test-key" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected authentication headers")
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["model"] != "decision-1" || request["state"] != "hello" {
			t.Errorf("request=%#v", request)
		}
		if len(request) != 3 {
			t.Errorf("request has unexpected top-level fields: %#v", request)
		}
		questions, ok := request["questions"].(map[string]any)
		if !ok || len(questions) != 1 {
			t.Fatalf("questions=%#v", request["questions"])
		}
		selection, ok := questions["selection"].(map[string]any)
		if !ok || len(selection) != 3 || selection["type"] != "choice" || selection["instructions"] == "" {
			t.Fatalf("invalid choice question: %#v", questions["selection"])
		}
		criteria, ok := selection["criteria"].(map[string]any)
		if !ok || len(criteria) != 2 || !strings.HasPrefix(criteria["candidate_0"].(string), "model=safe, effort=low. ") || !strings.HasPrefix(criteria["candidate_1"].(string), "model=strong, effort=high. ") {
			t.Fatalf("invalid criteria: %#v", selection["criteria"])
		}
		_, _ = w.Write([]byte(`{"answers":{"selection":{"type":"choice","choice":"candidate_1","confidence":0.99,"probabilities":{"candidate_0":0.005,"candidate_1":0.995}}},"model":"microsoft-decision-1","usage":{"tokens":3}}`))
	}))
	defer server.Close()
	for _, endpoint := range []string{
		"https://router.test/base",
		"https://router.test/base/",
		"https://router.test/base/providers/microsoft/v1/systemone",
		"https://router.test/base/providers/microsoft/v1/systemone/",
	} {
		t.Run(endpoint, func(t *testing.T) {
			got, ok := routeDecision(context.Background(), fixtureClient(server), endpoint, "test-key", "decision-1", "hello", allowed, fallback, 0.8)
			if !ok || got.Model != "strong" || got.Effort != "high" {
				t.Fatalf("got %#v, %v", got, ok)
			}
		})
	}
}

func TestRouteSelectionUsesSharedEffortsAndMultipleDeployments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request systemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		criteria := request.Questions["selection"].Criteria
		found := map[string]bool{}
		for _, description := range criteria {
			found[strings.SplitN(description, ". ", 2)[0]] = true
		}
		if !found["model=strong, effort=high"] || !found["model=safe, effort=low"] || found["model=safe, effort=high"] || found["model=strong, effort=unsupported"] {
			t.Errorf("incorrect shared/overridden efforts: %#v", criteria)
		}
		_, _ = w.Write([]byte(`{"answers":{"selection":{"choice":{"model":"strong","effort":"high"},"confidence":0.99}}}`))
	}))
	defer server.Close()
	previousClient := routingHTTPClient
	routingHTTPClient = fixtureClient(server)
	t.Cleanup(func() { routingHTTPClient = previousClient })
	e := Engine{Config: config.Config{SystemOne: config.SystemOneConfig{Endpoint: "https://router.test", APIKey: "test-key", Model: "decision-1", MinConfidence: 0.8}}}
	p := policy.Policy{
		Global:              policy.Capabilities{Models: []string{"safe", "strong"}, ThinkingLevels: []string{"low", "high", "unsupported"}},
		ModelThinkingLevels: map[string][]string{"safe": {"low"}},
	}
	got, ok := e.routeSelection(context.Background(), "hello", p, policy.Selection{Model: "safe"})
	if !ok || got.Model != "strong" || got.Effort != "high" {
		t.Fatalf("multiple-deployment selection failed: %#v %v", got, ok)
	}
}

func TestRouteSelectionHasFourChoicesAndSeparateFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request systemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		criteria := request.Questions["selection"].Criteria
		if len(criteria) != 4 {
			t.Errorf("expected exactly four choices, got %#v", criteria)
		}
		for _, description := range criteria {
			if !strings.Contains(description, "effort=medium.") && !strings.Contains(description, "effort=xhigh.") {
				t.Errorf("unexpected implicit effort: %q", description)
			}
		}
		_, _ = w.Write([]byte(`{"answers":{"selection":{"choice":"candidate_3","confidence":0.2}}}`))
	}))
	defer server.Close()
	previousClient := routingHTTPClient
	routingHTTPClient = fixtureClient(server)
	t.Cleanup(func() { routingHTTPClient = previousClient })
	e := Engine{Config: config.Config{SystemOne: config.SystemOneConfig{Endpoint: "https://router.test", APIKey: "test-key", Model: "decision-1", MinConfidence: 0.6}}}
	p := policy.Policy{Global: policy.Capabilities{Models: []string{"safe", "strong"}, ThinkingLevels: []string{"medium", "xhigh"}}}
	got, ok := e.routeSelection(context.Background(), "hello", p, policy.Selection{Model: "safe"})
	if ok || got.Model != "safe" || got.Effort != "" || got.HTTPStatus != 200 || got.FallbackReason != "low_confidence" || got.CandidateCount != 4 {
		t.Fatalf("fallback should be independent of decision choices: %#v %v", got, ok)
	}
}

func TestRouteDecisionFallsBackForInvalidResponsesAndHTTPFailures(t *testing.T) {
	fallback := routingChoice{Model: "safe", Effort: "low"}
	allowed := []routingCandidate{{Model: "safe", Effort: "low"}, {Model: "strong", Effort: "high"}}
	for _, test := range []struct{ body, reason string }{
		{`{"answers":{"selection":{"choice":"candidate_1","confidence":0.2}}}`, "low_confidence"},
		{`{"answers":{"selection":{"choice":"candidate_1"}}}`, "invalid_confidence"},
		{`{"answers":{"selection":{"choice":"candidate_99","confidence":0.9}}}`, "invalid_choice"},
		{`{"answers":{"selection":{"choice":"candidate_1","confidence":"NaN"}}}`, "invalid_confidence"},
		{`{"answers":{}}`, "missing_selection"},
		{`{bad json}`, "invalid_response"},
		{strings.Repeat("x", routingMaxBody+1), "response_too_large"},
	} {
		t.Run(test.reason, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test", "key", "decision-1", "hello", allowed, fallback, 0.8)
			if ok || got.Model != fallback.Model || got.Effort != fallback.Effort || got.FallbackReason != test.reason || got.HTTPStatus != 200 {
				t.Fatalf("fallback=%#v,%v", got, ok)
			}
			if test.reason == "low_confidence" && (got.Confidence == nil || *got.Confidence != 0.2) {
				t.Fatalf("fallback lost the returned confidence: %#v", got)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test", "key", "decision-1", strings.Repeat("x", routingMaxPrompt+1), allowed, fallback, 0.8)
	if ok || got.Model != fallback.Model {
		t.Fatalf("expected fallback %#v %v", got, ok)
	}
	got, ok = routeDecision(context.Background(), fixtureClient(server), "https://router.test", "key", "decision-1", "hello", allowed, fallback, 0.8)
	if ok || got.FallbackReason != "http_status" || got.HTTPStatus != 503 {
		t.Fatalf("expected HTTP diagnostic, got %#v %v", got, ok)
	}
}

func TestRouteDecisionReportsSafeTransportFailure(t *testing.T) {
	for _, test := range []struct {
		err    error
		reason string
	}{
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "cancelled"},
		{errors.New("sensitive URL and credential"), "transport_error"},
	} {
		t.Run(test.reason, func(t *testing.T) {
			client := &http.Client{Transport: routingErrorTransport{err: test.err}}
			fallback := routingChoice{Model: "safe"}
			got, ok := routeDecision(context.Background(), client, "https://router.test", "secret", "decision-1", "hello", []routingCandidate{{Model: "safe"}, {Model: "strong"}}, fallback, 0.8)
			if ok || got.Model != fallback.Model || got.FallbackReason != test.reason || got.HTTPStatus != 0 {
				t.Fatalf("unexpected diagnostic %#v %v", got, ok)
			}
		})
	}
}

type routingErrorTransport struct{ err error }

func (t routingErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

func TestRouteDecisionAllowsUnsetEffortAndHandlesNamedChoice(t *testing.T) {
	fallback := routingChoice{Model: "safe"}
	allowed := []routingCandidate{{Model: "safe"}, {Model: "strong", Effort: "high"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"selection":{"choice":{"model":"safe","effort":""},"confidence":"0.9"}}}`))
	}))
	defer server.Close()
	got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test", "key", "decision-1", "hello", allowed, fallback, 0.5)
	if !ok || got.Model != "safe" || got.Effort != "" {
		t.Fatalf("unset effort rejected: %#v %v", got, ok)
	}
}

func TestRoutingHTTPClientDoesNotFollowRedirects(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/providers/microsoft/v1/systemone" {
			http.Redirect(w, r, "/destination", http.StatusTemporaryRedirect)
			return
		}
		called = true
	}))
	defer server.Close()
	fallback := routingChoice{Model: "safe", Effort: "low"}
	got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test", "secret", "decision-1", "hello", []routingCandidate{{Model: "safe", Effort: "low"}, {Model: "strong", Effort: "high"}}, fallback, 0.5)
	if ok || got.Model != fallback.Model || called {
		t.Fatalf("redirect followed: %#v %v called=%v", got, ok, called)
	}
}

func TestRouteDecisionSkipsSingleUniqueCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("single-candidate routing must not call the service")
	}))
	defer server.Close()
	fallback := routingChoice{Model: "safe"}
	for _, candidates := range [][]routingCandidate{
		{{Model: "safe"}},
		{{Model: "safe"}, {Model: "safe"}},
	} {
		got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test", "key", "decision-1", "hello", candidates, fallback, 0.8)
		if ok || got.Model != fallback.Model || got.FallbackReason != "insufficient_candidates" || got.HTTPStatus != 0 {
			t.Fatalf("unexpected single-candidate result: %#v %v", got, ok)
		}
	}
}

type routingFixtureTransport struct {
	target string
	base   http.RoundTripper
}

func (t routingFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copyReq := req.Clone(req.Context())
	copyURL := *req.URL
	local, _ := url.Parse(t.target)
	copyURL.Scheme, copyURL.Host = local.Scheme, local.Host
	copyReq.URL = &copyURL
	return t.base.RoundTrip(copyReq)
}
func fixtureClient(server *httptest.Server) *http.Client {
	return &http.Client{Transport: routingFixtureTransport{target: server.URL, base: http.DefaultTransport}, CheckRedirect: routingHTTPClient.CheckRedirect}
}
