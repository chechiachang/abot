package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouteDecisionOnlyAcceptsExactAllowedPair(t *testing.T) {
	allowed := []routingCandidate{{Model: "safe", Effort: "low"}, {Model: "strong", Effort: "high"}}
	fallback := routingChoice{Model: "safe", Effort: "low"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth header missing")
		}
		var request routingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "system-one-decision-1" || len(request.Prompt) > routingMaxPrompt || len(request.Candidates) != 2 {
			t.Errorf("bad bounded request: %#v", request)
		}
		_, _ = w.Write([]byte(`{"model":"strong","effort":"high","confidence":0.99}`))
	}))
	defer server.Close()
	got, ok := routeDecision(context.Background(), server.Client(), server.URL, "test-key", "hello", allowed, fallback, 0.8)
	if !ok || got.Model != "strong" || got.Effort != "high" {
		t.Fatalf("got %#v, %v", got, ok)
	}

	for _, invalid := range []string{
		`{"model":"strong","effort":"low","confidence":0.99}`,
		`{"model":"evil","effort":"high","confidence":0.99}`,
		`{"model":"strong","effort":"high","confidence":0.2}`,
		`{"model":"strong","effort":"high","confidence":0.99,"action":"run"}`,
	} {
		t.Run(strings.ReplaceAll(invalid, "/", "_"), func(t *testing.T) {
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(invalid)) }))
			defer bad.Close()
			got, ok := routeDecision(context.Background(), bad.Client(), bad.URL, "key", "hello", allowed, fallback, 0.8)
			if ok || got != fallback {
				t.Fatalf("invalid decision accepted: %#v %v", got, ok)
			}
		})
	}
}

func TestRouteDecisionFallsBackForOversizedPromptAndErrors(t *testing.T) {
	fallback := routingChoice{Model: "safe", Effort: "low"}
	allowed := []routingCandidate{{Model: "safe", Effort: "low"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	for _, prompt := range []string{"hello", strings.Repeat("x", routingMaxPrompt+1)} {
		got, ok := routeDecision(context.Background(), server.Client(), server.URL, "key", prompt, allowed, fallback, 0.5)
		if ok || got != fallback {
			t.Fatalf("expected deterministic fallback: %#v %v", got, ok)
		}
	}
}
