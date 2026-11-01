package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
		_, _ = w.Write([]byte(`{"answers":{"selection":{"choice":"candidate_1","confidence":0.99}},"probabilities":{},"type":"decision","usage":{"tokens":3}}`))
	}))
	defer server.Close()
	got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test/base", "test-key", "decision-1", "hello", allowed, fallback, 0.8)
	if !ok || got.Model != "strong" || got.Effort != "high" {
		t.Fatalf("got %#v, %v", got, ok)
	}
}

func TestRouteDecisionFallsBackForInvalidResponsesAndHTTPFailures(t *testing.T) {
	fallback := routingChoice{Model: "safe", Effort: "low"}
	allowed := []routingCandidate{{Model: "safe", Effort: "low"}, {Model: "strong", Effort: "high"}}
	for _, body := range []string{
		`{"answers":{"selection":{"choice":"candidate_1","confidence":0.2}}}`,
		`{"answers":{"selection":{"choice":"candidate_1"}}}`,
		`{"answers":{"selection":{"choice":"candidate_99","confidence":0.9}}}`,
		`{"answers":{"selection":{"choice":"candidate_1","confidence":"NaN"}}}`,
		`{"answers":{}}`, `{bad json}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test", "key", "decision-1", "hello", allowed, fallback, 0.8)
			if ok || got.Model != fallback.Model || got.Effort != fallback.Effort {
				t.Fatalf("fallback=%#v,%v", got, ok)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test", "key", "decision-1", strings.Repeat("x", routingMaxPrompt+1), allowed, fallback, 0.8)
	if ok || got.Model != fallback.Model {
		t.Fatalf("expected fallback %#v %v", got, ok)
	}
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
	got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test", "secret", "decision-1", "hello", []routingCandidate{{Model: "safe", Effort: "low"}}, fallback, 0.5)
	if ok || got.Model != fallback.Model || called {
		t.Fatalf("redirect followed: %#v %v called=%v", got, ok, called)
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
