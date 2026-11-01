package engine

import (
	"context"
	"encoding/json"
	"github.com/chechiachang/abot/internal/policy"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func testPolicy() policy.Policy {
	return policy.Policy{Global: policy.Capabilities{Models: []string{"m1", "m2"}, ThinkingLevels: []string{"low", "high"}}, ModelThinkingLevels: map[string][]string{"m1": {"low"}, "m2": {"high"}}}
}
func TestRecommendAcceptsOnlyListedPair(t *testing.T) {
	p := testPolicy()
	base := policy.Selection{Model: "m1", ThinkingLevel: "low", Tools: []string{"read"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req routeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if len(req.Options) != 2 {
			t.Fatalf("options=%v", req.Options)
		}
		_ = json.NewEncoder(w).Encode(routeResponse{"m2", "high"})
	}))
	defer srv.Close()
	got := recommend(context.Background(), RouterConfig{Enabled: true, Endpoint: srv.URL, Timeout: time.Second}, "question", base, p)
	if got.Model != "m2" || got.ThinkingLevel != "high" || got.Tools[0] != "read" {
		t.Fatalf("got %+v", got)
	}
}
func TestRecommendFallsBackForInvalidAndFailures(t *testing.T) {
	p := testPolicy()
	base := policy.Selection{Model: "m1", ThinkingLevel: "low"}
	for _, tc := range []struct {
		code int
		body string
	}{{200, `{"model":"outside","effort":"high"}`}, {500, `{}`}, {200, `not json`}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); _, _ = w.Write([]byte(tc.body)) }))
		got := recommend(context.Background(), RouterConfig{Enabled: true, Endpoint: srv.URL, Timeout: time.Second}, "q", base, p)
		srv.Close()
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("got %+v want %+v", got, base)
		}
	}
}
func TestRecommendTimeoutAndDisabled(t *testing.T) {
	p := testPolicy()
	base := policy.Selection{Model: "m1"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer srv.Close()
	got := recommend(context.Background(), RouterConfig{Enabled: true, Endpoint: srv.URL, Timeout: time.Millisecond}, "q", base, p)
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("timeout got %+v", got)
	}
	got = recommend(context.Background(), RouterConfig{}, "q", base, p)
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("disabled got %+v", got)
	}
}
