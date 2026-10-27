package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsAPIKeyInConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("azure:\n  api_key: do-not-store-this\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v := NewViper()
	v.Set("config", path)
	_, err := Load(v)
	var configError *Error
	if !errors.As(err, &configError) || !strings.Contains(err.Error(), "do not store secrets") || strings.Contains(err.Error(), "do-not-store-this") {
		t.Fatalf("config secret error = %v", err)
	}
}

func TestLoadRejectsTelegramSecretsInConfigFile(t *testing.T) {
	for _, contents := range []string{"telegram:\n  bot_token: bot-secret\n", "telegram:\n  morsel_api_key: morsel-secret\n", "telegram:\n  r2_access_key_id: access-secret\n", "telegram:\n  r2_secret_access_key: secret-value\n", "logfire:\n  token: trace-secret\n"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		v := NewViper()
		v.Set("config", path)
		_, err := Load(v)
		var configError *Error
		if !errors.As(err, &configError) || !strings.Contains(err.Error(), "do not store secrets") || strings.Contains(err.Error(), "bot-secret") || strings.Contains(err.Error(), "morsel-secret") || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("config secret error = %v", err)
		}
	}
}
