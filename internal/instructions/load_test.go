package instructions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSystemAndOptionalSoul(t *testing.T) {
	dir := t.TempDir()
	system := filepath.Join(dir, "SYSTEM.md")
	soul := filepath.Join(dir, "SOUL.md")
	if err := os.WriteFile(system, []byte("system"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(system, soul)
	if err != nil || got != "system" {
		t.Fatalf("missing optional soul: got %q, err %v", got, err)
	}
	if err := os.WriteFile(soul, []byte("persona"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = Load(system, soul)
	if err != nil || got != "system\n\npersona" {
		t.Fatalf("instruction order: got %q, err %v", got, err)
	}
}
