package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckSQLitePathCreatesWritablePrivateStateDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "runtime.sqlite")
	if err := checkSQLitePath(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("state directory mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := os.WriteFile(path, []byte("SQLite format 3\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkSQLitePath(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkSQLitePath(path); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid database error=%v", err)
	}
}
