package urls

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPublicTargetValidationAndPrivateRedirectRejection(t *testing.T) {
	resolver := mapResolver{"public.test": {netip.MustParseAddr("93.184.216.34")}}
	reader := testReader(t, resolver)
	for _, target := range []string{"http://127.0.0.1/", "http://10.1.2.3/", "http://169.254.169.254/", "http://[::1]/", "file:///etc/passwd", "https://user:pw@example.test/"} {
		if _, _, err := reader.validateURL(context.Background(), target); err == nil {
			t.Errorf("target %q unexpectedly passed validation", target)
		}
	}
	if _, _, err := reader.validateURL(context.Background(), "https://public.test/page#section"); err != nil {
		t.Fatalf("public URL with fragment: %v", err)
	}
	resolver["private.test"] = []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.2")}
	if _, _, err := reader.validateURL(context.Background(), "https://private.test/"); errorCategory(err) != "private_target" {
		t.Fatalf("mixed public/private DNS error=%v", err)
	}
	reader.resolver = mapResolver{"public.test": {netip.MustParseAddr("93.184.216.34")}, "private.test": {netip.MustParseAddr("10.0.0.2")}}
	reader.fetch = func(context.Context, string, string, http.Header, []byte) (response, error) {
		return response{status: http.StatusFound, location: "http://private.test/"}, nil
	}
	if _, err := reader.Read(context.Background(), "https://public.test/start"); errorCategory(err) != "private_target" {
		t.Fatalf("private redirect error=%v", err)
	}
}

func TestPublicIPClassification(t *testing.T) {
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888"} {
		if !publicIP(netip.MustParseAddr(address)) {
			t.Errorf("public address %s was blocked", address)
		}
	}
	for _, address := range []string{"0.1.2.3", "100.64.1.2", "192.0.2.1", "198.18.0.1", "224.0.0.1", "fc00::1", "2001:db8::1", "fe80::1"} {
		if publicIP(netip.MustParseAddr(address)) {
			t.Errorf("special address %s was allowed", address)
		}
	}
}

func TestReadHTMLNormalizesContentAndStripsExecutableMarkup(t *testing.T) {
	reader := testReader(t, mapResolver{"example.test": {netip.MustParseAddr("93.184.216.34")}})
	reader.fetch = func(context.Context, string, string, http.Header, []byte) (response, error) {
		return response{status: http.StatusOK, contentType: "text/html; charset=utf-8", body: []byte(`<html><head><title>Useful page</title><script>secret()</script></head><body><h1>Heading</h1><p>First   paragraph.</p><p>Second paragraph.</p></body></html>`)}, nil
	}
	result, err := reader.Read(context.Background(), "https://example.test/article")
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "Useful page" || result.Source != "web" || !strings.Contains(result.Content, "First paragraph.") || strings.Contains(result.Content, "secret()") {
		t.Fatalf("read result=%#v", result)
	}
}

func TestFirecrawlRequiresExplicitExternalTransferAndReportsSourceErrors(t *testing.T) {
	resolver := mapResolver{
		"example.test":   {netip.MustParseAddr("93.184.216.34")},
		"firecrawl.test": {netip.MustParseAddr("93.184.216.34")},
	}
	reader := testReader(t, resolver)
	var calls int
	reader.config.FirecrawlEndpoint = "https://firecrawl.test/v1/scrape"
	reader.config.FirecrawlAPIKey = "test-secret"
	reader.fetch = func(_ context.Context, method, target string, headers http.Header, _ []byte) (response, error) {
		calls++
		if strings.Contains(target, "example.test") {
			return response{status: http.StatusServiceUnavailable}, nil
		}
		if method != http.MethodPost || headers.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("Firecrawl request method=%q authorization=%q", method, headers.Get("Authorization"))
		}
		return response{status: http.StatusOK, body: []byte(`{"success":true,"data":{"markdown":"article","metadata":{"title":"Title"}}}`)}, nil
	}
	if _, err := reader.Read(context.Background(), "https://example.test/article"); errorCategory(err) != "http_status" || calls != 1 {
		t.Fatalf("disabled Firecrawl error=%v calls=%d", err, calls)
	}
	reader.config.FirecrawlExternalTransfer = true
	result, err := reader.Read(context.Background(), "https://example.test/article")
	if err != nil || result.Source != "firecrawl" || result.Title != "Title" || result.Content != "article" || calls != 3 {
		t.Fatalf("Firecrawl result=%#v calls=%d err=%v", result, calls, err)
	}
}

func TestReadHonorsCancellationAndCaptionParser(t *testing.T) {
	reader := testReader(t, mapResolver{"example.test": {netip.MustParseAddr("93.184.216.34")}})
	reader.fetch = func(ctx context.Context, _, _ string, _ http.Header, _ []byte) (response, error) {
		<-ctx.Done()
		return response{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.Read(ctx, "https://example.test/page"); errorCategory(err) != "cancelled" {
		t.Fatalf("cancelled read error=%v", err)
	}
	if got := parseCaptions([]byte(`<transcript><text>hello &amp; welcome</text><text>from captions</text></transcript>`)); got != "hello & welcome from captions" {
		t.Fatalf("caption text=%q", got)
	}
}

func TestDialRevalidatesDNSAndRestrictedProxyRejectsPrivateTargets(t *testing.T) {
	reader := testReader(t, &sequenceResolver{answers: [][]netip.Addr{
		{netip.MustParseAddr("93.184.216.34")},
		{netip.MustParseAddr("93.184.216.34")},
		{netip.MustParseAddr("10.0.0.2")},
	}})
	reader.fetch = reader.fetchHTTP
	if _, err := reader.Read(context.Background(), "https://rebinding.test/"); errorCategory(err) != "private_target" {
		t.Fatalf("rebound DNS target error=%v", err)
	}

	reader.resolver = mapResolver{"private.test": {netip.MustParseAddr("10.0.0.2")}}
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
	_, _ = fmt.Fprint(connection, "CONNECT private.test:443 HTTP/1.1\r\nHost: private.test:443\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("private CONNECT status=%d", response.StatusCode)
	}
}

func TestTruncatePreservesUTF8(t *testing.T) {
	if got := truncate("你好世界", 5); !strings.HasPrefix(got, "你") || !utf8.ValidString(got) || len(got) > 5 {
		t.Fatalf("truncated text=%q", got)
	}
}

type mapResolver map[string][]netip.Addr

func (m mapResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	addresses, ok := m[host]
	if !ok {
		return nil, errors.New("not found")
	}
	return addresses, nil
}

type sequenceResolver struct {
	answers [][]netip.Addr
	index   int
}

func (r *sequenceResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if r.index >= len(r.answers) {
		return nil, errors.New("no DNS answer")
	}
	answer := r.answers[r.index]
	r.index++
	return answer, nil
}

func testReader(t *testing.T, resolver Resolver) *Reader {
	t.Helper()
	reader, err := NewReader(Config{Timeout: time.Second, MaxBytes: 1 << 20, MaxOutputBytes: 4096, MaxRedirects: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reader.resolver = resolver
	return reader
}

func errorCategory(err error) string {
	var sourceErr *Error
	if errors.As(err, &sourceErr) {
		return sourceErr.Category
	}
	return ""
}
