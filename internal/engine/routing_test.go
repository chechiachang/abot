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
		if request.Model != "decision-test" || len(request.Prompt) > routingMaxPrompt || len(request.Candidates) != 2 {
			t.Errorf("bad bounded request: %#v", request)
		}
		_, _ = w.Write([]byte(`{"model":"strong","effort":"high","confidence":0.99}`))
	}))
	defer server.Close()
	got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test/v1/route", "test-key", "decision-test", "hello", allowed, fallback, 0.8)
	if !ok || got.Model != "strong" || got.Effort != "high" {
		t.Fatalf("got %#v, %v", got, ok)
	}

	for _, invalid := range []string{
		`{"model":"strong","effort":"low","confidence":0.99}`,
		`{"model":"evil","effort":"high","confidence":0.99}`,
		`{"model":"strong","effort":"high","confidence":0.2}`,
		`{"model":"strong","effort":"high","confidence":0.99,"action":"run"}`,
		`{"model":"strong","effort":"high"}`,
		`{"model":"strong","effort":"high","confidence":null}`,
	} {
		t.Run(strings.ReplaceAll(invalid, "/", "_"), func(t *testing.T) {
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(invalid)) }))
			defer bad.Close()
			got, ok := routeDecision(context.Background(), fixtureClient(bad), "https://router.test/v1/route", "key", "decision-test", "hello", allowed, fallback, 0.8)
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
		got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test/v1/route", "key", "decision-test", prompt, allowed, fallback, 0.5)
		if ok || got != fallback {
			t.Fatalf("expected deterministic fallback: %#v %v", got, ok)
		}
	}
}

func TestRouteDecisionAllowsUnsetEffortAndHonorsConfiguredModel(t *testing.T) {
	fallback := routingChoice{Model: "safe", Effort: ""}
	allowed := []routingCandidate{{Model: "safe", Effort: ""}, {Model: "strong", Effort: "high"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request routingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "custom-router" {
			t.Errorf("configured model ignored: %q", request.Model)
		}
		_, _ = w.Write([]byte(`{"model":"safe","effort":"","confidence":0.9}`))
	}))
	defer server.Close()
	got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test/v1/route", "key", "custom-router", "hello", allowed, fallback, 0.5)
	if !ok || got != fallback {
		t.Fatalf("unset-effort choice not accepted: %#v %v", got, ok)
	}
}

func TestRoutingHTTPClientDoesNotFollowRedirects(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/destination", http.StatusTemporaryRedirect)
			return
		}
		called = true
	}))
	defer server.Close()
	fallback := routingChoice{Model: "safe", Effort: "low"}
	got, ok := routeDecision(context.Background(), fixtureClient(server), "https://router.test/redirect", "secret", "router", "hello", []routingCandidate{{Model: "safe", Effort: "low"}}, fallback, 0.5)
	if ok || got != fallback || called {
		t.Fatalf("redirect should not be followed: %#v %v called=%v", got, ok, called)
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
