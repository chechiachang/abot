package logging

import "testing"

func TestRedactShortSecret(t *testing.T) {
	if got := Redact("key=value", "key"); got != "[REDACTED]=value" {
		t.Fatalf("short secret was not removed: %q", got)
	}
}
