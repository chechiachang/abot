package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAzureFlagOverridesNestedConfig(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "test-key")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("azure:\n  base_url: https://file.example/openai/v1\n  model: file-model\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", path, "--azure-base-url", "invalid", "config", "validate"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "azure.base_url") {
		t.Fatalf("azure-base-url flag was not applied: %v", err)
	}
}

func TestPolicyExportReviewApplyAndAudit(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	contents := "azure:\n  base_url: https://example.test/openai/v1\n  model: model-a\ntelegram:\n  allowed_user_ids: [10]\n  default_group_id: -100\ncapabilities:\n  global:\n    models: [model-a]\n    tools: [read]\n  defaults:\n    model: model-a\n    tools: [read]\n"
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZURE_OPENAI_API_KEY", "provider-secret")
	policyPath := filepath.Join(dir, "state", "policy.json")
	auditPath := filepath.Join(dir, "state", "audit.jsonl")
	proposal := filepath.Join(dir, "proposal.json")
	for _, args := range [][]string{
		{"--config", configPath, "--policy-file", policyPath, "--audit-file", auditPath, "policy", "export", proposal},
		{"--config", configPath, "--policy-file", policyPath, "--audit-file", auditPath, "policy", "review", proposal},
		{"--config", configPath, "--policy-file", policyPath, "--audit-file", auditPath, "policy", "apply", proposal},
		{"--config", configPath, "--policy-file", policyPath, "--audit-file", auditPath, "policy", "validate"},
	} {
		cmd := NewRootCmd()
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("command %q failed: %v", args[len(args)-2:], err)
		}
	}
	audit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(audit), "provider-secret") || !strings.Contains(string(audit), "policy.apply") {
		t.Fatalf("audit output = %s", audit)
	}
}

func TestProviderInspectionMapsReasoningFlagWithoutExposingKey(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "provider-secret")
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "azure:\n  base_url: https://example.test/openai/v1\n  model: model-a\ncapabilities:\n  global:\n    thinking_levels: [none]\n  defaults:\n    model: model-a\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--config", path, "--azure-reasoning-effort", "none", "provider", "show"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "reasoning_effort=none") || strings.Contains(output.String(), "provider-secret") {
		t.Fatalf("provider output = %q", output.String())
	}
}
