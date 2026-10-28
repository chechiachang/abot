package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
func TestR2UploaderStoresRandomObjectAndSignsOnDemand(t *testing.T) {
	f := &fakeR2{link: "https://example.test/signed"}
	u := newR2Uploader(f, "bucket", "abot", 20*time.Minute, 100)
	first, err := u.Store(context.Background(), "long content")
	if err != nil {
		t.Fatal(err)
	}
	if string(f.content) != "long content" || f.contentType != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected upload %#v", f)
	}
	if !strings.HasPrefix(first, "bucket/abot/") || first != f.locator {
		t.Fatalf("locator %q; stored %q", first, f.locator)
	}
	got, err := u.Presign(context.Background(), first, 20*time.Minute)
	if err != nil || got != f.link || f.expiry != 20*time.Minute {
		t.Fatalf("signed link=%q expiry=%s err=%v", got, f.expiry, err)
	}
	second, err := u.Store(context.Background(), "long content")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("keys not random")
	}
}
func TestR2UploaderLimitsAndSafeErrors(t *testing.T) {
	f := &fakeR2{putErr: errors.New("credential leaked")}
	u := newR2Uploader(f, "bucket", "", time.Minute, 3)
	if _, err := u.Store(context.Background(), "four"); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("error %v", err)
	}
	u.maxBytes = 10
	if _, err := u.Store(context.Background(), "fine"); err == nil || strings.Contains(err.Error(), "credential") {
		t.Fatalf("unsafe error %v", err)
	}
}
func TestTelegramMessageLimitCountsCharacters(t *testing.T) {
	if utf8.RuneCountInString(strings.Repeat("🙂", 4096)) != TelegramTextLimit {
		t.Fatal("message limit mismatch")
	}
}
