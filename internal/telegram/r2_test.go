package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeR2 struct {
	locator     string
	content     []byte
	contentType string
	expiry      time.Duration
	link        string
	putErr      error
}

func (f *fakeR2) Put(_ context.Context, locator string, body []byte, contentType string) error {
	f.locator, f.content, f.contentType = locator, body, contentType
	return f.putErr
}
func (f *fakeR2) PresignGet(_ context.Context, locator string, expiry time.Duration) (string, error) {
	f.locator, f.expiry = locator, expiry
	return f.link, nil
}
func TestR2UploaderPublishesRandomObject(t *testing.T) {
	f := &fakeR2{link: "https://example.test/signed"}
	u := newR2Uploader(f, "bucket", "abot", 20*time.Minute, 100)
	got, err := u.Publish(context.Background(), "long content")
	if err != nil {
		t.Fatal(err)
	}
	if got != f.link || string(f.content) != "long content" || f.contentType != "text/plain; charset=utf-8" || f.expiry != 20*time.Minute {
		t.Fatalf("unexpected upload %#v", f)
	}
	first := f.locator
	if !strings.HasPrefix(first, "bucket/abot/") {
		t.Fatalf("locator %q", f.locator)
	}
	if _, err := u.Publish(context.Background(), "long content"); err != nil {
		t.Fatal(err)
	}
	if first == f.locator {
		t.Fatal("keys not random")
	}
}
func TestR2UploaderLimitsAndSafeErrors(t *testing.T) {
	f := &fakeR2{putErr: errors.New("credential leaked")}
	u := newR2Uploader(f, "bucket", "", time.Minute, 3)
	if _, err := u.Publish(context.Background(), "four"); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("error %v", err)
	}
	u.maxBytes = 10
	if _, err := u.Publish(context.Background(), "fine"); err == nil || strings.Contains(err.Error(), "credential") {
		t.Fatalf("unsafe error %v", err)
	}
}

func TestTelegramMessageSizeUsesUTF16Units(t *testing.T) {
	if got := telegramMessageSize(strings.Repeat("🙂", 3)); got != 6 {
		t.Fatalf("emoji size=%d UTF-16 units; want 6", got)
	}
	if telegramMessageSize(strings.Repeat("🙂", 2048)) != TelegramTextLimit {
		t.Fatal("expected 2048 supplementary characters to equal the 4096-unit API limit")
	}
	if telegramMessageSize("界") != 1 {
		t.Fatal("BMP character should use one UTF-16 code unit")
	}
}

func TestPrepareAnswerUnicodeHardLimitAndPublisherFallback(t *testing.T) {
	p := &testPublisher{link: "https://example.test/full"}
	got, _, err := PrepareAnswer(context.Background(), strings.Repeat("🙂", DefaultTelegramOffloadThreshold/2+1), p)
	if err != nil || p.calls != 1 || !strings.Contains(got, p.link) {
		t.Fatalf("UTF-16 boundary did not publish: calls=%d output=%q err=%v", p.calls, got, err)
	}
}

func TestPrepareAnswerOffloadBoundaryIndependentOfUXPreference(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantLink bool
	}{
		{"at UX threshold stays inline", strings.Repeat("a", LongOutputThreshold), false},
		{"above UX threshold remains inline", strings.Repeat("a", LongOutputThreshold+1), false},
		{"at offload threshold stays inline", strings.Repeat("a", DefaultTelegramOffloadThreshold), false},
		{"above offload threshold uploads", strings.Repeat("a", DefaultTelegramOffloadThreshold+1), true},
		{"supplementary unicode crosses threshold", strings.Repeat("🙂", DefaultTelegramOffloadThreshold/2+1), true},
		{"below threshold with supplementary unicode inline", strings.Repeat("🙂", DefaultTelegramOffloadThreshold/2), false},
		{"at Telegram max safely offloads", strings.Repeat("a", TelegramTextLimit), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &testPublisher{link: "https://example.test/full"}
			got, _, err := PrepareAnswer(context.Background(), tt.text, p)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantLink && (p.calls != 1 || !strings.Contains(got, p.link)) {
				t.Fatalf("did not offload: calls=%d output=%q", p.calls, got)
			}
			if !tt.wantLink && (p.calls != 0 || telegramMessageSize(tt.text) > DefaultTelegramOffloadThreshold) {
				t.Fatalf("unexpected inline output: calls=%d units=%d", p.calls, telegramMessageSize(tt.text))
			}
		})
	}
}

type testPublisher struct {
	link  string
	calls int
}

func (p *testPublisher) Publish(_ context.Context, _, _ string) (string, error) {
	p.calls++
	return p.link, nil
}
