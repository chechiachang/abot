package logging

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestRedactSensitiveURLPartsAndSecrets(t *testing.T) {
	got := Redact("https://user:pass@example.com/path?api_key=abc&ok=1 Bearer token-value", "top-secret")
	want := "https://[REDACTED]@example.com/path?api_key=[REDACTED]&ok=1 Bearer [REDACTED]"
	if got != want {
		t.Fatalf("Redact() = %q, want %q", got, want)
	}
	if got := Redact("request top-secret failed", "top-secret"); got != "request [REDACTED] failed" {
		t.Fatalf("secret was not removed: %q", got)
	}
}

func TestHookRedactsMessagesAndFields(t *testing.T) {
	entry := &logrus.Entry{
		Message: "bad url https://user:pass@example.com/?token=hidden",
		Data: logrus.Fields{
			"api_key": "a-real-secret",
			"detail":  "Bearer a-real-secret",
		},
	}
	if err := (&redactionHook{secrets: []string{"a-real-secret"}}).Fire(entry); err != nil {
		t.Fatal(err)
	}
	if entry.Message != "bad url https://[REDACTED]@example.com/?token=[REDACTED]" {
		t.Fatalf("message not redacted: %q", entry.Message)
	}
	if entry.Data["api_key"] != "[REDACTED]" || entry.Data["detail"] != "Bearer [REDACTED]" {
		t.Fatalf("fields not redacted: %#v", entry.Data)
	}
}
