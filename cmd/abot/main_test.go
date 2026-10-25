package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	const loadedKey = "ABOT_TEST_DOTENV_LOADED"
	restoreEnv(t, loadedKey)
	if err := os.Unsetenv(loadedKey); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABOT_TEST_DOTENV_PRESET", "process")

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(loadedKey+"=from-file\nABOT_TEST_DOTENV_PRESET=from-file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(loadedKey); got != "from-file" {
		t.Fatalf("loaded value = %q", got)
	}
	if got := os.Getenv("ABOT_TEST_DOTENV_PRESET"); got != "process" {
		t.Fatalf("existing process value was overridden: %q", got)
	}
	if err := loadDotEnv(filepath.Join(t.TempDir(), ".env")); err != nil {
		t.Fatalf("missing .env should be optional: %v", err)
	}
}

func restoreEnv(t *testing.T, key string) {
	t.Helper()
	value, exists := os.LookupEnv(key)
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
