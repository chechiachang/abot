package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
)

func TestOTLPExporterSendsBearerAuthAndFlushes(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/traces" || r.Header.Get("Authorization") != "Bearer logfire-test-token" {
			t.Errorf("export request path=%q authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	shutdown, err := Start(context.Background(), server.URL+"/v1/traces", "logfire-test-token", "")
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "sample")
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("export requests=%d", requests)
	}
}

func TestTracingDisabledWithoutToken(t *testing.T) {
	shutdown, err := Start(context.Background(), "", "", "")
	if err != nil || shutdown == nil {
		t.Fatalf("shutdown available=%t err=%v", shutdown != nil, err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
