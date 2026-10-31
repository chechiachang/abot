package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/chechiachang/abot/internal/config"
)

type fakeR2 struct {
	locator         string
	body            []byte
	expiry          time.Duration
	putErr, signErr error
	signs           int
}

func (f *fakeR2) Put(_ context.Context, locator string, body []byte) error {
	f.locator, f.body = locator, body
	return f.putErr
}
func (f *fakeR2) PresignGet(_ context.Context, locator string, expiry time.Duration) (string, error) {
	f.locator, f.expiry = locator, expiry
	f.signs++
	return fmt.Sprintf("https://r2.example/responses_for_users/(part).txt?X-Amz-Signature=secret-%d&X-Amz-Expires=%d", f.signs, int(expiry/time.Second)), f.signErr
}
func testR2(f *fakeR2) *R2Uploader {
	return &R2Uploader{store: f, bucket: "abot-share", expiry: time.Hour, maxBytes: 5 << 20}
}

func TestR2RandomKeysLimitsAndSafeErrors(t *testing.T) {
	f := &fakeR2{}
	u := testR2(f)
	first, err := u.Store(context.Background(), "你好")
	if err != nil || string(f.body) != "你好" || !strings.HasPrefix(first, "abot-share/abot/responses/") || f.signs != 0 {
		t.Fatalf("upload=%q err=%v", first, err)
	}
	second, err := u.Store(context.Background(), "你好")
	if err != nil || second == first {
		t.Fatal("object keys must be unique")
	}
	u.maxBytes = 5
	if _, err := u.Store(context.Background(), "你好"); err == nil {
		t.Fatal("limit must count bytes")
	}
	u.maxBytes = 100
	f.putErr = errors.New("credential-secret")
	if _, err := u.Store(context.Background(), "answer"); err == nil || strings.Contains(err.Error(), "credential-secret") {
		t.Fatalf("unsafe upload error %v", err)
	}
	f.signErr = errors.New("credential-secret")
	if _, err := u.Presign(context.Background(), first, time.Minute); err == nil || strings.Contains(err.Error(), "credential-secret") {
		t.Fatalf("unsafe signing error %v", err)
	}
	for _, locator := range []string{"other/abot/responses/a.txt", "abot-share/other/a.txt", "abot-share/abot/responses/../a.txt"} {
		if _, err := u.Presign(context.Background(), locator, time.Minute); err == nil {
			t.Errorf("accepted %q", locator)
		}
	}
	for _, expiry := range []time.Duration{0, 24 * time.Hour} {
		if _, err := u.Presign(context.Background(), first, expiry); err == nil {
			t.Errorf("accepted expiry %v", expiry)
		}
	}
}

func TestR2SDKUploadAndSignedURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "PUT" || r.URL.Path != "/abot-share/abot/responses/test.txt" || string(body) != "plain answer" || r.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("upload method=%s path=%s body=%s", r.Method, r.URL.Path, body)
		}
		if r.Header.Get("X-Amz-Server-Side-Encryption") != "" {
			t.Error("unsupported SSE header")
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("unsigned upload")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := s3.NewFromConfig(aws.Config{Region: "auto", Credentials: credentials.NewStaticCredentialsProvider("test-id", "test-secret", ""), HTTPClient: server.Client()}, func(o *s3.Options) { o.BaseEndpoint = aws.String(server.URL); o.UsePathStyle = true })
	store := &s3ObjectStore{client: client}
	if err := store.Put(context.Background(), "abot-share/abot/responses/test.txt", []byte("plain answer")); err != nil {
		t.Fatal(err)
	}
	link, err := store.PresignGet(context.Background(), "abot-share/abot/responses/test.txt", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(link)
	if parsed.Query().Get("X-Amz-Signature") == "" || parsed.Query().Get("X-Amz-Expires") != "3600" || !strings.Contains(parsed.Query().Get("X-Amz-Credential"), "/auto/s3/aws4_request") {
		t.Fatalf("invalid signed URL %s", link)
	}
}

func TestR2DisabledAndPartialCredentials(t *testing.T) {
	u, err := NewR2Uploader(config.R2Config{})
	if err != nil || u != nil {
		t.Fatalf("disabled uploader=%v err=%v", u, err)
	}
	if _, err := NewR2Uploader(config.R2Config{AccessKeyID: "secret"}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("partial credential error=%v", err)
	}
}

func TestR2RoutingUnicodeBoundaries(t *testing.T) {
	for _, char := range []string{"x", "中", "😀"} {
		units := telegramMessageSize(char)
		for _, limit := range []int{1000, 3500} {
			inline := strings.Repeat(char, limit/units)
			if shouldOffloadR2(inline, limit) || !shouldOffloadR2(inline+char, limit) {
				t.Errorf("boundary for %q at %d", char, limit)
			}
		}
	}
	if !shouldOffloadR2(strings.Repeat("x", 3501), 4000) {
		t.Error("safety ceiling bypassed")
	}
	if !shouldOffloadR2(strings.Repeat("x", 501), 500) || shouldOffloadR2(strings.Repeat("x", 1500), 2000) {
		t.Error("inline preference ineffective")
	}
}

func TestR2LinkEscaping(t *testing.T) {
	got, err := renderHTMLLink("Open <response>", "https://r2.example/responses_for_users/(part).txt?a=1&b=2")
	if err != nil || !strings.Contains(got, `href="https://r2.example/responses_for_users/(part).txt?a=1&amp;b=2"`) || !strings.Contains(got, "Open &lt;response&gt;") {
		t.Fatalf("link=%s err=%v", got, err)
	}
	for _, link := range []string{"http://example.com", "javascript:alert(1)", "https://user:secret@example.com"} {
		if _, err := renderHTMLLink("open", link); err == nil {
			t.Errorf("accepted %s", link)
		}
	}
}

func TestLegacyMorselUnicodeBoundaries(t *testing.T) {
	for _, char := range []string{"x", "中", "😀"} {
		publisher := &countingArticlePublisher{}
		if _, _, err := PrepareAnswer(context.Background(), strings.Repeat(char, 1000), publisher); err != nil || publisher.calls != 0 {
			t.Fatalf("legacy inline boundary for %q: %v", char, err)
		}
		if _, _, err := PrepareAnswer(context.Background(), strings.Repeat(char, 1001), publisher); err != nil || publisher.calls != 1 {
			t.Fatalf("legacy publication boundary for %q: %v", char, err)
		}
	}
}
