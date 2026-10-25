package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTelegramClientPollSendAndHideTokenFromErrors(t *testing.T) {
	var updatesRequested bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/botsecret-token/") {
			t.Errorf("API path = %q", r.URL.Path)
		}
		switch r.URL.Path {
		case "/botsecret-token/getUpdates":
			updatesRequested = true
			var request Updates
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Offset != 10 || request.Timeout != 25 {
				t.Errorf("poll request=%#v err=%v", request, err)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":10,"message":{"message_id":2,"chat":{"id":4,"type":"private"},"from":{"id":4,"is_bot":false},"text":"hello"}}]}`))
		case "/botsecret-token/sendMessage":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":88,"chat":{"id":4,"type":"private"}}}`))
		case "/botsecret-token/fail":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"secret-token was rejected"}`))
		}
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("secret-token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	updates, err := client.GetUpdates(context.Background(), 10, 25)
	if err != nil || !updatesRequested || len(updates) != 1 || updates[0].Message.Text != "hello" {
		t.Fatalf("updates=%#v requested=%t err=%v", updates, updatesRequested, err)
	}
	sent, err := client.SendMessage(context.Background(), SendMessage{ChatID: 4, Text: "hi", ParseMode: "HTML"})
	if err != nil || sent.MessageID != 88 {
		t.Fatalf("sent=%#v err=%v", sent, err)
	}
	err = client.call(context.Background(), "fail", struct{}{}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.Fatal() || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("safe API error=%v", err)
	}
}

func TestTelegramRetryDelayAndFatalPollerErrors(t *testing.T) {
	delay, retry := retryDelay(&APIError{Code: http.StatusTooManyRequests, RetryAfter: 7 * time.Second}, 0)
	if !retry || delay != 7*time.Second {
		t.Fatalf("rate-limit retry delay=%s retry=%t", delay, retry)
	}
	if _, retry := retryDelay(&APIError{Code: http.StatusConflict}, 1); retry {
		t.Fatal("competing-poller conflict should be fatal")
	}
}

func TestTelegramGetAndBoundDownloadFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bottoken/getFile":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"file","file_size":4,"file_path":"photos/file.jpg"}}`))
		case "/file/bottoken/photos/file.jpg":
			_, _ = w.Write([]byte("data"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	file, err := client.GetFile(context.Background(), "file")
	if err != nil || file.FilePath != "photos/file.jpg" || file.FileSize != 4 {
		t.Fatalf("file=%#v err=%v", file, err)
	}
	data, err := client.DownloadFile(context.Background(), file.FilePath, 4)
	if err != nil || string(data) != "data" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if _, err := client.DownloadFile(context.Background(), file.FilePath, 3); err == nil {
		t.Fatal("expected file download size limit to fail")
	}
	if _, err := client.DownloadFile(context.Background(), "../secret", 10); err == nil {
		t.Fatal("expected invalid remote file path to fail")
	}
}

func TestRenderHTMLEscapesUntrustedMarkupAndAllowsLimitedFormatting(t *testing.T) {
	rendered := RenderHTML("**bold** `<tag>`\n<script>x</script> [safe](https://example.test/path)")
	for _, want := range []string{"<b>bold</b>", "<code>&lt;tag&gt;</code>", "&lt;script&gt;x&lt;/script&gt;", `<a href="https://example.test/path">safe</a>`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered HTML %q missing %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "<script>") {
		t.Fatalf("untrusted raw HTML passed through: %q", rendered)
	}
}

type fakePublisher struct {
	text string
	url  string
}

func (p *fakePublisher) Publish(_ context.Context, _, text string) (string, error) {
	p.text = text
	return p.url, nil
}

func TestPrepareAnswerUsesMorselAboveOneThousandCharacters(t *testing.T) {
	content := strings.Repeat("有", 1001)
	publisher := &fakePublisher{url: "https://morsel.example/post/1"}
	text, mode, err := PrepareAnswer(context.Background(), content, publisher)
	if err != nil || mode != "HTML" || publisher.text != content || !strings.Contains(text, "morsel.example/post/1") {
		t.Fatalf("prepared answer=%q mode=%q err=%v", text, mode, err)
	}
	if _, _, err := PrepareAnswer(context.Background(), content, nil); err == nil || !strings.Contains(err.Error(), "MORSEL_API_KEY") {
		t.Fatalf("missing Morsel configuration error=%v", err)
	}
}

func TestMorselPublisherUsesConfiguredEndpointAndDoesNotLeakKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer morsel-secret" {
			t.Errorf("authorization header = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["content"] != "long" {
			t.Errorf("Morsel body=%#v err=%v", body, err)
		}
		_, _ = w.Write([]byte(`{"url":"https://morsel.example/x"}`))
	}))
	defer server.Close()
	client, err := NewMorselClient(server.URL, "morsel-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	url, err := client.Publish(context.Background(), "title", "long")
	if err != nil || url != "https://morsel.example/x" {
		t.Fatalf("published URL=%q err=%v", url, err)
	}
}
