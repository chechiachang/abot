package urls

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestBrowserFallbackIsOptionalAndSubrequestsStayBehindPublicProxy(t *testing.T) {
	reader := testReader(t, mapResolver{
		"public.example":  {netip.MustParseAddr("93.184.216.34")},
		"private.example": {netip.MustParseAddr("10.0.0.10")},
	})
	reader.fetch = func(context.Context, string, string, http.Header, []byte) (response, error) {
		return response{status: http.StatusServiceUnavailable}, nil
	}
	if retryWithBrowser(&Error{Category: "private_target"}) || retryWithBrowser(&Error{Category: "timeout"}) {
		t.Fatal("browser fallback must not run after a security or deadline failure")
	}
	reader.config.BrowserExecutable = "/missing/chromium"
	if _, err := reader.Read(context.Background(), "https://public.example/blocked"); errorCategory(err) != "unsupported_source" {
		t.Fatalf("missing optional browser error=%v", err)
	}

	reader.resolver = mapResolver{"private.example": {netip.MustParseAddr("10.0.0.10")}}
	proxy, stop, err := reader.startHTTPSProxy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	connection, err := net.Dial("tcp", strings.TrimPrefix(proxy, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, _ = fmt.Fprint(connection, "CONNECT private.example:443 HTTP/1.1\r\nHost: private.example:443\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("browser private subrequest status=%d", response.StatusCode)
	}
}

func TestProxyTransferBudgetStopsOversizedTunnels(t *testing.T) {
	budget := &transferBudget{limit: 3}
	reader := budgetReader{Reader: strings.NewReader("payload"), budget: budget}
	buffer := make([]byte, 8)
	if _, err := reader.Read(buffer); err == nil || budget.used.Load() <= budget.limit {
		t.Fatalf("budget allowed oversized tunnel: used=%d err=%v", budget.used.Load(), err)
	}
}
