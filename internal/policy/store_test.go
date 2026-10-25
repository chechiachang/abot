package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyIsAtomicValidatedAndAuditedWithoutPolicySecrets(t *testing.T) {
	store := Store{PolicyPath: filepath.Join(t.TempDir(), "state", "policy.json")}
	store.AuditPath = filepath.Join(filepath.Dir(store.PolicyPath), "audit.jsonl")
	valid := testPolicy()
	valid.Grants = []Grant{{Kind: "channel", ID: -200, GrantedBy: "api-key-do-not-log", GrantedAt: "today"}}
	if err := store.Apply(valid, "admin"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.PolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Defaults.Model = "unavailable"
	if err := store.Apply(invalid, "admin"); err == nil {
		t.Fatal("expected invalid policy apply to fail")
	}
	after, err := os.ReadFile(store.PolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("invalid policy changed active policy")
	}
	audit, err := os.ReadFile(store.AuditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(audit), "api-key-do-not-log") || !strings.Contains(string(audit), "policy.apply") {
		t.Fatalf("audit contains unexpected data: %s", audit)
	}
	info, err := os.Stat(store.PolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("policy permissions = %o", info.Mode().Perm())
	}
}

func TestLoadFileRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"credentials":{"api_key":"secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("load error = %v", err)
	}
}
