package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chechiachang/abot/internal/policy"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func TestLoadPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("azure:\n  model: file-model\n  base_url: https://file.example/openai/v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABOT_AZURE_MODEL", "env-model")
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://env.example/openai/v1")
	t.Setenv("AZURE_OPENAI_REASONING_EFFORT", "none")
	v := NewViper()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("azure-model", "", "")
	if err := v.BindPFlag("azure.model", flags.Lookup("azure-model")); err != nil {
		t.Fatal(err)
	}
	v.Set("config", path)
	cfg, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Azure.Model != "env-model" {
		t.Fatalf("environment should override config file: got %q", cfg.Azure.Model)
	}
	if cfg.Azure.BaseURL != "https://env.example/openai/v1" {
		t.Fatalf("environment URL should override config file: got %q", cfg.Azure.BaseURL)
	}
	if cfg.Azure.ReasoningEffort != "none" {
		t.Fatalf("reasoning effort environment value = %q", cfg.Azure.ReasoningEffort)
	}

	if err := flags.Set("azure-model", "flag-model"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(newViperWithFlag(flags, path))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Azure.Model != "flag-model" {
		t.Fatalf("flag should override environment: got %q", cfg.Azure.Model)
	}
}

func TestLoadRejectsUnsupportedReasoningEffort(t *testing.T) {
	t.Setenv("ABOT_AZURE_REASONING_EFFORT", "extreme")
	if _, err := Load(NewViper()); err == nil {
		t.Fatal("expected unsupported reasoning effort to fail")
	}
}

func newViperWithFlag(flags *pflag.FlagSet, path string) *viper.Viper {
	v := NewViper()
	_ = v.BindPFlag("azure.model", flags.Lookup("azure-model"))
	v.Set("config", path)
	return v
}

func TestValidateProviderRequiresExplicitModelAndSecret(t *testing.T) {
	cfg := Config{Azure: AzureConfig{BaseURL: "https://example.com/openai/v1", Timeout: 1}}
	if err := cfg.ValidateProvider(); err == nil {
		t.Fatal("expected missing key/model to fail")
	}
	cfg.Azure.APIKey = "secret"
	cfg.Azure.Model = "deployment"
	if err := cfg.ValidateProvider(); err != nil {
		t.Fatalf("valid provider config: %v", err)
	}
}

func TestLoadTelegramAuthorizationFromEnvironment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ABOT_AZURE_BASE_URL", "https://example.test/openai/v1")
	t.Setenv("ABOT_AZURE_MODEL", "model-a")
	t.Setenv("ABOT_TELEGRAM_ALLOWED_USER_IDS", "10,20")
	t.Setenv("ABOT_TELEGRAM_DEFAULT_GROUP_ID", "-100")
	cfg, err := Load(NewViper())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Telegram.AllowedUserIDs) != 2 || cfg.Telegram.AllowedUserIDs[0] != 10 || cfg.Telegram.DefaultGroupID != -100 {
		t.Fatalf("telegram config = %#v", cfg.Telegram)
	}
}

func TestLoadValidatesMCPTransportsAndCredentialReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := `azure:
  base_url: https://example.test/openai/v1
  model: model-a
capabilities:
  global:
    models: [model-a]
    mcp: [chrome-devtools, firecrawl]
  defaults:
    model: model-a
mcp:
  connections:
    - name: chrome-devtools
      transport: stdio
      command: npx
      args: ["-y", "chrome-devtools-mcp@latest"]
      environment: [CHROME_PROFILE]
      timeout: 30s
      max_retries: 1
    - name: firecrawl
      transport: http
      endpoint: https://firecrawl.example/mcp
      header_env:
        Authorization: FIRECRAWL_API_KEY
`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	v := NewViper()
	v.Set("config", path)
	cfg, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCP.Connections) != 2 || cfg.MCP.Connections[1].HeaderEnv["authorization"] != "FIRECRAWL_API_KEY" {
		t.Fatalf("MCP config = %#v", cfg.MCP)
	}
}

func TestValidateMCPRejectsUnconfiguredPolicyAndEmbeddedEndpointCredentials(t *testing.T) {
	base := Config{Azure: AzureConfig{Model: "model"}, Capabilities: policy.ConfiguredCapabilities{
		Global:   policy.Capabilities{Models: []string{"model"}, MCP: []string{"missing"}},
		Defaults: policy.Selection{Model: "model"},
	}}
	if err := base.EffectivePolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := base.ValidateMCP(); err == nil {
		t.Fatal("expected unconfigured MCP policy connection to fail")
	}
	base.Capabilities.Global.MCP = []string{"http"}
	base.MCP.Connections = []MCPConnection{{Name: "http", Transport: "http", Endpoint: "https://user:secret@example.test/mcp"}}
	if err := base.ValidateMCP(); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("embedded credentials error = %v", err)
	}
}

func TestTelegramResponseInlineThresholdValidation(t *testing.T) {
	base := Config{Log: LogConfig{Level: "info"}, Azure: AzureConfig{Timeout: 1, MaxRetries: 0}, Coding: CodingConfig{CommandTimeout: 1, MaxOutputBytes: 1}, Runtime: RuntimeConfig{TurnTimeout: 1}, Media: MediaConfig{Root: ".", MaxBytes: 1024, MaxDuration: time.Second, MaxPages: 1, MaxExtractBytes: 1024, Retention: time.Hour}, URLReader: URLReaderConfig{Timeout: time.Second, MaxBytes: 1024, MaxOutputBytes: 1024, MaxRedirects: 0}, Instructions: InstructionConfig{SystemFile: "SYSTEM.md"}, Capabilities: policy.ConfiguredCapabilities{Global: policy.Capabilities{Models: []string{"m"}, Tools: []string{"read"}}, Defaults: policy.Selection{Model: "m", Tools: []string{"read"}}}}
	for _, value := range []int{1, 1000, TelegramResponseHardLimit - 1} {
		cfg := base
		cfg.Telegram.ResponseInlineThreshold = value
		if err := cfg.Validate(); err != nil {
			t.Errorf("threshold %d should validate: %v", value, err)
		}
	}
	for _, value := range []int{-1, TelegramResponseHardLimit, TelegramResponseHardLimit + 1} {
		cfg := base
		cfg.Telegram.ResponseInlineThreshold = value
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "response_inline_threshold") {
			t.Errorf("threshold %d: expected response threshold validation error; got %v", value, err)
		}
	}
}
