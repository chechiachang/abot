package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadR2(t *testing.T) {
	t.Setenv("ABOT_AZURE_MODEL", "test-model")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("r2:\n  endpoint: https://account.r2.cloudflarestorage.com\n  bucket: abot-share\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABOT_R2_ACCESS_KEY_ID", "test-id")
	t.Setenv("ABOT_R2_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("ABOT_R2_BUCKET", "env-bucket")
	v := NewViper()
	v.Set("config", path)
	cfg, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.R2.Endpoint != "https://account.r2.cloudflarestorage.com" || cfg.R2.Bucket != "env-bucket" || cfg.R2.Region != "auto" || cfg.R2.PresignExpiry != time.Hour || cfg.R2.AccessKeyID != "test-id" || cfg.R2.SecretAccessKey != "test-secret" {
		t.Fatal("R2 file settings, environment overrides, or defaults were not loaded")
	}
	cfg.R2.AccessKeyID, cfg.R2.SecretAccessKey = "", ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("credentials may be filled in later: %v", err)
	}
	for _, endpoint := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com?token=secret", "https://example.com/path"} {
		cfg.R2.Endpoint = endpoint
		if err := cfg.Validate(); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("endpoint validation error = %v", err)
		}
	}
	cfg.R2.Endpoint = "https://account.r2.cloudflarestorage.com"
	for _, expiry := range []time.Duration{0, -time.Hour, 24 * time.Hour} {
		cfg.R2.PresignExpiry = expiry
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted invalid expiry %v", expiry)
		}
	}
}

func TestLoadRejectsR2CredentialsInConfig(t *testing.T) {
	for _, key := range []string{"access_key_id", "secret_access_key"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("r2:\n  "+key+": private-value\n"), 0600); err != nil {
			t.Fatal(err)
		}
		v := NewViper()
		v.Set("config", path)
		if _, err := Load(v); err == nil || !strings.Contains(err.Error(), "do not store secrets") || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("credential rejection error = %v", err)
		}
	}
}

func TestR2OptionalExampleAndValidation(t *testing.T) {
	t.Setenv("ABOT_AZURE_MODEL", "test-model")
	t.Setenv("ABOT_R2_ACCESS_KEY_ID", "")
	t.Setenv("ABOT_R2_SECRET_ACCESS_KEY", "")
	v := NewViper()
	v.Set("config", "../../config.example.yaml")
	cfg, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.R2.Enabled() || cfg.R2.MaxBytes != 5<<20 || cfg.Telegram.ResponseInlineThreshold != 1000 {
		t.Fatal("invalid optional defaults")
	}
	cfg.R2.AccessKeyID = "private-value"
	if err := cfg.Validate(); err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("partial credentials error=%v", err)
	}
	cfg.R2.AccessKeyID = ""
	for _, max := range []int64{0, -1, 50<<20 + 1} {
		cfg.R2.MaxBytes = max
		if err := cfg.Validate(); err == nil {
			t.Errorf("accepted max=%d", max)
		}
	}
	cfg.R2.MaxBytes = 5 << 20
	for _, threshold := range []int{0, -1, 3501} {
		cfg.Telegram.ResponseInlineThreshold = threshold
		if err := cfg.Validate(); err == nil {
			t.Errorf("accepted threshold=%d", threshold)
		}
	}
}
