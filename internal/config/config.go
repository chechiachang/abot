package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chechiachang/abot/internal/policy"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type Config struct {
	Azure        AzureConfig                   `mapstructure:"azure"`
	Coding       CodingConfig                  `mapstructure:"coding"`
	Instructions InstructionConfig             `mapstructure:"instructions"`
	Log          LogConfig                     `mapstructure:"log"`
	Telegram     TelegramConfig                `mapstructure:"telegram"`
	Capabilities policy.ConfiguredCapabilities `mapstructure:"capabilities"`
	PolicyFile   string                        `mapstructure:"policy_file"`
	AuditFile    string                        `mapstructure:"audit_file"`
	Runtime      RuntimeConfig                 `mapstructure:"runtime"`
	Skills       SkillsConfig                  `mapstructure:"skills"`
	MCP          MCPConfig                     `mapstructure:"mcp"`
	Media        MediaConfig                   `mapstructure:"media"`
	R2           R2Config                      `mapstructure:"r2"`
	URLReader    URLReaderConfig               `mapstructure:"url_reader"`
	Logfire      LogfireConfig                 `mapstructure:"logfire"`
}

type AzureConfig struct {
	BaseURL         string        `mapstructure:"base_url"`
	APIKey          string        `mapstructure:"api_key"`
	Model           string        `mapstructure:"model"`
	ReasoningEffort string        `mapstructure:"reasoning_effort"`
	MaxRetries      int           `mapstructure:"max_retries"`
	Timeout         time.Duration `mapstructure:"timeout"`
}

type InstructionConfig struct {
	SystemFile string `mapstructure:"system_file"`
	SoulFile   string `mapstructure:"soul_file"`
}

type CodingConfig struct {
	Workdir        string        `mapstructure:"workdir"`
	CommandTimeout time.Duration `mapstructure:"command_timeout"`
	MaxOutputBytes int64         `mapstructure:"max_output_bytes"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

type RuntimeConfig struct {
	SQLitePath  string        `mapstructure:"sqlite_path"`
	TurnTimeout time.Duration `mapstructure:"turn_timeout"`
}

type SkillsConfig struct {
	Paths []string `mapstructure:"paths"`
}

const DefaultResponseInlineThreshold = 1000

type TelegramConfig struct {
	ResponseInlineThreshold int     `mapstructure:"response_inline_threshold"`
	BotToken                string  `mapstructure:"bot_token"`
	AdminChatID             int64   `mapstructure:"admin_chat_id"`
	MorselAPIKey            string  `mapstructure:"morsel_api_key"`
	MorselPublishURL        string  `mapstructure:"morsel_publish_url"`
	AllowedUserIDs          []int64 `mapstructure:"allowed_user_ids"`
	AdminUserIDs            []int64 `mapstructure:"admin_user_ids"`
	DefaultGroupID          int64   `mapstructure:"default_group_id"`
	ApprovedChannelIDs      []int64 `mapstructure:"approved_channel_ids"`
}

type MCPConfig struct {
	Connections []MCPConnection `mapstructure:"connections"`
}

type MCPConnection struct {
	Name        string            `mapstructure:"name"`
	Transport   string            `mapstructure:"transport"`
	Command     string            `mapstructure:"command"`
	Args        []string          `mapstructure:"args"`
	Environment []string          `mapstructure:"environment"`
	Endpoint    string            `mapstructure:"endpoint"`
	HeaderEnv   map[string]string `mapstructure:"header_env"`
	Timeout     time.Duration     `mapstructure:"timeout"`
	MaxRetries  int               `mapstructure:"max_retries"`
}

type MediaConfig struct {
	Root            string        `mapstructure:"root"`
	MaxBytes        int64         `mapstructure:"max_bytes"`
	MaxDuration     time.Duration `mapstructure:"max_duration"`
	MaxPages        int           `mapstructure:"max_pages"`
	MaxExtractBytes int           `mapstructure:"max_extract_bytes"`
	Retention       time.Duration `mapstructure:"retention"`
	Pandoc          string        `mapstructure:"pandoc"`
	PDFInfo         string        `mapstructure:"pdfinfo"`
	PDFToText       string        `mapstructure:"pdftotext"`
	FFmpeg          string        `mapstructure:"ffmpeg"`
	FFprobe         string        `mapstructure:"ffprobe"`
	Whisper         string        `mapstructure:"whisper"`
	WhisperModel    string        `mapstructure:"whisper_model"`
	YouTubeDL       string        `mapstructure:"youtube_dl"`
}

type URLReaderConfig struct {
	Timeout                   time.Duration `mapstructure:"timeout"`
	MaxBytes                  int64         `mapstructure:"max_bytes"`
	MaxOutputBytes            int           `mapstructure:"max_output_bytes"`
	MaxRedirects              int           `mapstructure:"max_redirects"`
	BrowserExecutable         string        `mapstructure:"browser"`
	FirecrawlExternalTransfer bool          `mapstructure:"firecrawl_external_transfer"`
	FirecrawlEndpoint         string        `mapstructure:"firecrawl_endpoint"`
	FirecrawlAPIKey           string        `mapstructure:"firecrawl_api_key"`
}

type R2Config struct {
	Endpoint        string        `mapstructure:"endpoint"`
	Bucket          string        `mapstructure:"bucket"`
	Region          string        `mapstructure:"region"`
	PresignExpiry   time.Duration `mapstructure:"presign_expiry"`
	MaxBytes        int64         `mapstructure:"max_bytes"`
	AccessKeyID     string        `mapstructure:"access_key_id"`
	SecretAccessKey string        `mapstructure:"secret_access_key"`
}

type LogfireConfig struct {
	Endpoint    string `mapstructure:"endpoint"`
	Environment string `mapstructure:"environment"`
	Token       string `mapstructure:"token"`
}

func (c TelegramConfig) Policy() policy.Telegram {
	return policy.Telegram{AllowedUserIDs: c.AllowedUserIDs, AdminUserIDs: c.AdminUserIDs, DefaultGroupID: c.DefaultGroupID, ApprovedChannelIDs: c.ApprovedChannelIDs}
}

func NewViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("ABOT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	for key, names := range map[string][]string{
		"azure.base_url":                         {"ABOT_AZURE_BASE_URL", "AZURE_OPENAI_BASE_URL", "AZURE_ENDPOINT"},
		"azure.api_key":                          {"ABOT_AZURE_API_KEY", "AZURE_OPENAI_API_KEY"},
		"azure.model":                            {"ABOT_AZURE_MODEL", "AZURE_OPENAI_MODEL", "AZURE_OPENAI_DEPLOYMENT"},
		"azure.reasoning_effort":                 {"ABOT_AZURE_REASONING_EFFORT", "AZURE_OPENAI_REASONING_EFFORT"},
		"azure.timeout":                          {"ABOT_AZURE_TIMEOUT", "AZURE_OPENAI_TIMEOUT"},
		"azure.max_retries":                      {"ABOT_AZURE_MAX_RETRIES", "AZURE_OPENAI_MAX_RETRIES"},
		"policy_file":                            {"ABOT_POLICY_FILE"},
		"audit_file":                             {"ABOT_AUDIT_FILE"},
		"telegram.allowed_user_ids":              {"ABOT_TELEGRAM_ALLOWED_USER_IDS"},
		"telegram.admin_user_ids":                {"ABOT_TELEGRAM_ADMIN_USER_IDS"},
		"telegram.default_group_id":              {"ABOT_TELEGRAM_DEFAULT_GROUP_ID"},
		"telegram.approved_channel_ids":          {"ABOT_TELEGRAM_APPROVED_CHANNEL_IDS"},
		"telegram.bot_token":                     {"ABOT_TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN"},
		"telegram.admin_chat_id":                 {"ABOT_TELEGRAM_ADMIN_CHAT_ID"},
		"telegram.morsel_api_key":                {"ABOT_MORSEL_API_KEY", "MORSEL_API_KEY"},
		"telegram.morsel_publish_url":            {"ABOT_MORSEL_PUBLISH_URL", "MORSEL_PUBLISH_URL"},
		"capabilities.global.tools":              {"ABOT_CAPABILITIES_GLOBAL_TOOLS"},
		"capabilities.global.models":             {"ABOT_CAPABILITIES_GLOBAL_MODELS"},
		"capabilities.global.skills":             {"ABOT_CAPABILITIES_GLOBAL_SKILLS"},
		"capabilities.global.mcp":                {"ABOT_CAPABILITIES_GLOBAL_MCP"},
		"capabilities.global.thinking_levels":    {"ABOT_CAPABILITIES_GLOBAL_THINKING_LEVELS"},
		"capabilities.defaults.tools":            {"ABOT_CAPABILITIES_DEFAULTS_TOOLS"},
		"capabilities.defaults.model":            {"ABOT_CAPABILITIES_DEFAULTS_MODEL"},
		"capabilities.defaults.skills":           {"ABOT_CAPABILITIES_DEFAULTS_SKILLS"},
		"capabilities.defaults.mcp":              {"ABOT_CAPABILITIES_DEFAULTS_MCP"},
		"capabilities.defaults.thinking_level":   {"ABOT_CAPABILITIES_DEFAULTS_THINKING_LEVEL"},
		"runtime.sqlite_path":                    {"ABOT_RUNTIME_SQLITE_PATH"},
		"runtime.turn_timeout":                   {"ABOT_RUNTIME_TURN_TIMEOUT"},
		"skills.paths":                           {"ABOT_SKILLS_PATHS"},
		"media.root":                             {"ABOT_MEDIA_ROOT"},
		"media.max_bytes":                        {"ABOT_MEDIA_MAX_BYTES"},
		"media.max_duration":                     {"ABOT_MEDIA_MAX_DURATION"},
		"media.max_pages":                        {"ABOT_MEDIA_MAX_PAGES"},
		"media.max_extract_bytes":                {"ABOT_MEDIA_MAX_EXTRACT_BYTES"},
		"media.retention":                        {"ABOT_MEDIA_RETENTION"},
		"media.pandoc":                           {"ABOT_MEDIA_PANDOC"},
		"media.pdfinfo":                          {"ABOT_MEDIA_PDFINFO"},
		"media.pdftotext":                        {"ABOT_MEDIA_PDFTOTEXT"},
		"media.ffmpeg":                           {"ABOT_MEDIA_FFMPEG"},
		"media.ffprobe":                          {"ABOT_MEDIA_FFPROBE"},
		"media.whisper":                          {"ABOT_MEDIA_WHISPER"},
		"media.whisper_model":                    {"ABOT_MEDIA_WHISPER_MODEL"},
		"media.youtube_dl":                       {"ABOT_MEDIA_YOUTUBE_DL"},
		"telegram.response_inline_threshold":     {"ABOT_TELEGRAM_RESPONSE_INLINE_THRESHOLD"},
		"r2.max_bytes":                           {"ABOT_R2_MAX_BYTES"},
		"r2.endpoint":                            {"ABOT_R2_ENDPOINT"},
		"r2.bucket":                              {"ABOT_R2_BUCKET"},
		"r2.region":                              {"ABOT_R2_REGION"},
		"r2.presign_expiry":                      {"ABOT_R2_PRESIGN_EXPIRY"},
		"r2.access_key_id":                       {"ABOT_R2_ACCESS_KEY_ID"},
		"r2.secret_access_key":                   {"ABOT_R2_SECRET_ACCESS_KEY"},
		"url_reader.timeout":                     {"ABOT_URL_READER_TIMEOUT"},
		"url_reader.max_bytes":                   {"ABOT_URL_READER_MAX_BYTES"},
		"url_reader.max_output_bytes":            {"ABOT_URL_READER_MAX_OUTPUT_BYTES"},
		"url_reader.max_redirects":               {"ABOT_URL_READER_MAX_REDIRECTS"},
		"url_reader.browser":                     {"ABOT_URL_BROWSER"},
		"url_reader.firecrawl_external_transfer": {"ABOT_URL_FIRECRAWL_EXTERNAL_TRANSFER"},
		"url_reader.firecrawl_endpoint":          {"ABOT_URL_FIRECRAWL_ENDPOINT"},
		"url_reader.firecrawl_api_key":           {"FIRECRAWL_API_KEY"},
		"logfire.endpoint":                       {"ABOT_LOGFIRE_ENDPOINT"},
		"logfire.environment":                    {"LOGFIRE_ENVIRONMENT", "ABOT_LOGFIRE_ENVIRONMENT"},
		"logfire.token":                          {"LOGFIRE_TOKEN"},
	} {
		_ = v.BindEnv(append([]string{key}, names...)...)
	}
	v.SetDefault("azure.timeout", time.Minute)
	v.SetDefault("azure.max_retries", 2)
	v.SetDefault("coding.workdir", ".")
	v.SetDefault("coding.command_timeout", 2*time.Minute)
	v.SetDefault("coding.max_output_bytes", int64(65536))
	v.SetDefault("instructions.system_file", "SYSTEM.md")
	v.SetDefault("instructions.soul_file", "SOUL.md")
	v.SetDefault("log.level", "info")
	v.SetDefault("runtime.turn_timeout", 10*time.Minute)
	if path, err := defaultConfigPath(); err == nil {
		v.SetDefault("policy_file", filepath.Join(filepath.Dir(path), "policy.json"))
		v.SetDefault("audit_file", filepath.Join(filepath.Dir(path), "audit.jsonl"))
	}
	if path, err := defaultSQLitePath(); err == nil {
		v.SetDefault("runtime.sqlite_path", path)
	}
	v.SetDefault("skills.paths", defaultSkillPaths())
	v.SetDefault("media.max_bytes", int64(10<<20))
	v.SetDefault("media.max_duration", 5*time.Minute)
	v.SetDefault("media.max_pages", 50)
	v.SetDefault("media.max_extract_bytes", 1<<20)
	v.SetDefault("media.retention", 30*24*time.Hour)
	v.SetDefault("media.pandoc", "pandoc")
	v.SetDefault("media.pdfinfo", "pdfinfo")
	v.SetDefault("media.pdftotext", "pdftotext")
	v.SetDefault("media.ffmpeg", "ffmpeg")
	v.SetDefault("media.ffprobe", "ffprobe")
	v.SetDefault("telegram.response_inline_threshold", DefaultResponseInlineThreshold)
	v.SetDefault("r2.max_bytes", int64(5<<20))
	v.SetDefault("r2.region", "auto")
	v.SetDefault("r2.presign_expiry", time.Hour)
	v.SetDefault("url_reader.timeout", 30*time.Second)
	v.SetDefault("url_reader.max_bytes", int64(10<<20))
	v.SetDefault("url_reader.max_output_bytes", 12<<10)
	v.SetDefault("url_reader.max_redirects", 5)
	v.SetDefault("url_reader.firecrawl_endpoint", "https://api.firecrawl.dev/v1/scrape")
	v.SetDefault("logfire.endpoint", "https://logfire-api.pydantic.dev/v1/traces")
	if path, err := defaultMediaPath(); err == nil {
		v.SetDefault("media.root", path)
	}
	return v
}

func defaultSkillPaths() []string {
	paths := []string{".agents/skills"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".agents", "skills"))
	}
	return paths
}

func defaultSQLitePath() (string, error) {
	if stateDir := os.Getenv("XDG_STATE_HOME"); stateDir != "" {
		return filepath.Join(stateDir, "abot", "state.sqlite"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "abot", "state.sqlite"), nil
}

func defaultMediaPath() (string, error) {
	if dataDir := os.Getenv("XDG_DATA_HOME"); dataDir != "" {
		return filepath.Join(dataDir, "abot", "media"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "abot", "media"), nil
}

func Load(v *viper.Viper) (Config, error) {
	if path := v.GetString("config"); path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return Config{}, &Error{Field: "config", Cause: fmt.Errorf("read config: %w", err)}
		}
	} else if path, err := defaultConfigPath(); err == nil {
		if _, err := os.Stat(path); err == nil {
			v.SetConfigFile(path)
			if err := v.ReadInConfig(); err != nil {
				return Config{}, &Error{Field: "config", Cause: fmt.Errorf("read config: %w", err)}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return Config{}, &Error{Field: "config", Cause: fmt.Errorf("inspect config: %w", err)}
		}
	} else {
		return Config{}, &Error{Field: "config", Cause: err}
	}

	if v.InConfig("azure.api_key") || v.InConfig("azure.api-key") {
		return Config{}, invalid("azure.api_key", "do not store secrets in a config file; use AZURE_OPENAI_API_KEY")
	}
	for key, envName := range map[string]string{"telegram.bot_token": "TELEGRAM_BOT_TOKEN", "telegram.morsel_api_key": "MORSEL_API_KEY", "url_reader.firecrawl_api_key": "FIRECRAWL_API_KEY", "logfire.token": "LOGFIRE_TOKEN", "r2.access_key_id": "ABOT_R2_ACCESS_KEY_ID", "r2.secret_access_key": "ABOT_R2_SECRET_ACCESS_KEY"} {
		if v.InConfig(key) {
			return Config{}, invalid(key, "do not store secrets in a config file; use "+envName)
		}
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, &Error{Field: "config", Cause: fmt.Errorf("decode config: %w", err)}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaultConfigPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "abot", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "abot", "config.yaml"), nil
}

func (c Config) Validate() error {
	if _, err := logrus.ParseLevel(c.Log.Level); err != nil {
		return &Error{Field: "log.level", Cause: err}
	}
	if c.Azure.Timeout <= 0 {
		return invalid("azure.timeout", "must be positive")
	}
	if c.Azure.MaxRetries < 0 || c.Azure.MaxRetries > 5 {
		return invalid("azure.max_retries", "must be between 0 and 5")
	}
	switch c.Azure.ReasoningEffort {
	case "", "none", "low", "medium", "high":
	default:
		return invalid("azure.reasoning_effort", "must be none, low, medium, or high")
	}
	if c.Coding.CommandTimeout <= 0 || c.Coding.MaxOutputBytes <= 0 || c.Coding.MaxOutputBytes > 10<<20 {
		return invalid("coding", "command timeout and output limit must be positive; output is capped at 10 MiB")
	}
	if c.Runtime.TurnTimeout <= 0 || c.Runtime.TurnTimeout > 24*time.Hour {
		return invalid("runtime.turn_timeout", "must be positive and at most 24h")
	}
	if c.Media.MaxBytes < 1024 || c.Media.MaxBytes > 50<<20 || c.Media.MaxDuration <= 0 || c.Media.MaxDuration > time.Hour || c.Media.MaxPages < 1 || c.Media.MaxPages > 500 || c.Media.MaxExtractBytes < 1024 || c.Media.MaxExtractBytes > 10<<20 || c.Media.Retention < time.Hour || c.Media.Retention > 3650*24*time.Hour {
		return invalid("media", "attachment, duration, page, and extracted-output limits are outside their supported range")
	}
	if strings.TrimSpace(c.Media.Root) == "" {
		return invalid("media.root", "is required")
	}
	if c.Telegram.ResponseInlineThreshold < 1 || c.Telegram.ResponseInlineThreshold > 3500 {
		return invalid("telegram.response_inline_threshold", "must be between 1 and 3500 UTF-16 units")
	}
	if err := c.R2.Validate(); err != nil {
		return err
	}
	if c.URLReader.Timeout <= 0 || c.URLReader.Timeout > 10*time.Minute || c.URLReader.MaxBytes < 1024 || c.URLReader.MaxBytes > 50<<20 || c.URLReader.MaxOutputBytes < 1024 || c.URLReader.MaxOutputBytes > 64<<10 || c.URLReader.MaxRedirects < 0 || c.URLReader.MaxRedirects > 10 {
		return invalid("url_reader", "timeout, download, output, and redirect limits are outside their supported range")
	}
	if c.URLReader.FirecrawlEndpoint != "" {
		u, err := url.Parse(c.URLReader.FirecrawlEndpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return invalid("url_reader.firecrawl_endpoint", "must be an HTTP(S) URL without credentials or a fragment")
		}
	}
	if c.URLReader.FirecrawlExternalTransfer && strings.TrimSpace(c.URLReader.FirecrawlAPIKey) == "" {
		return invalid("url_reader.firecrawl_api_key", "FIRECRAWL_API_KEY is required when external transfer is explicitly enabled")
	}
	if c.Logfire.Endpoint != "" {
		u, err := url.Parse(c.Logfire.Endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return invalid("logfire.endpoint", "must be an HTTPS URL without credentials or a fragment")
		}
	}
	if c.Azure.BaseURL != "" {
		if err := validateBaseURL(c.Azure.BaseURL); err != nil {
			return &Error{Field: "azure.base_url", Cause: err}
		}
	}
	if c.Telegram.MorselAPIKey != "" && c.Telegram.MorselPublishURL == "" {
		return invalid("telegram.morsel_publish_url", "is required when MORSEL_API_KEY is configured")
	}
	if c.Telegram.MorselPublishURL != "" {
		u, err := url.Parse(strings.TrimSpace(c.Telegram.MorselPublishURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return invalid("telegram.morsel_publish_url", "must be an HTTP(S) URL without credentials, query, or fragment")
		}
	}
	if strings.TrimSpace(c.Instructions.SystemFile) == "" {
		return invalid("instructions.system_file", "is required")
	}
	if err := c.EffectivePolicy().Validate(); err != nil {
		return &Error{Field: "policy", Cause: err}
	}
	if err := c.ValidateMCP(); err != nil {
		return err
	}
	return nil
}

func (c Config) ValidateMCP() error {
	connections := make(map[string]bool, len(c.MCP.Connections))
	for i, connection := range c.MCP.Connections {
		field := fmt.Sprintf("mcp.connections[%d]", i)
		if strings.TrimSpace(connection.Name) == "" || strings.TrimSpace(connection.Name) != connection.Name {
			return invalid(field+".name", "must be a nonempty stable ID")
		}
		for _, char := range connection.Name {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
				return invalid(field+".name", "must contain lowercase letters, digits, hyphens, or underscores")
			}
		}
		if connections[connection.Name] {
			return invalid(field+".name", "must be unique")
		}
		connections[connection.Name] = true
		if connection.Timeout == 0 {
			connection.Timeout = 30 * time.Second
		}
		if connection.Timeout < time.Second || connection.Timeout > 10*time.Minute {
			return invalid(field+".timeout", "must be between 1s and 10m")
		}
		if connection.MaxRetries < 0 || connection.MaxRetries > 5 {
			return invalid(field+".max_retries", "must be between 0 and 5")
		}
		switch connection.Transport {
		case "stdio":
			if strings.TrimSpace(connection.Command) == "" || connection.Endpoint != "" {
				return invalid(field, "stdio transport requires command and forbids endpoint")
			}
			if len(connection.HeaderEnv) != 0 {
				return invalid(field+".header_env", "is only valid for HTTP transport")
			}
		case "http":
			u, err := url.Parse(connection.Endpoint)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
				return invalid(field+".endpoint", "must be an HTTP(S) URL without credentials or a fragment")
			}
			if connection.Command != "" || len(connection.Args) > 0 || len(connection.Environment) > 0 {
				return invalid(field, "HTTP transport forbids command, args, and environment")
			}
		default:
			return invalid(field+".transport", "must be stdio or http")
		}
		for _, environment := range connection.Environment {
			if !validEnvironmentName(environment) {
				return invalid(field+".environment", "must contain environment variable names only")
			}
		}
		for header, environment := range connection.HeaderEnv {
			if strings.TrimSpace(header) == "" || !validEnvironmentName(environment) {
				return invalid(field+".header_env", "maps HTTP header names to environment variable names")
			}
		}
	}
	for _, name := range c.EffectivePolicy().Global.MCP {
		if !connections[name] {
			return invalid("capabilities.global.mcp", fmt.Sprintf("connection %q is not configured", name))
		}
	}
	return nil
}

func validEnvironmentName(value string) bool {
	if value == "" || !(value[0] == '_' || value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for _, char := range value[1:] {
		if !(char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func (c Config) EffectivePolicy() policy.Policy {
	p := policy.Policy{
		Version: policy.Version, Telegram: c.Telegram.Policy(),
		Global: c.Capabilities.Global, Defaults: c.Capabilities.Defaults,
		ModelThinkingLevels: c.Capabilities.ModelThinkingLevels,
		ChatOverrides:       c.Capabilities.ChatOverrides, Grants: c.Capabilities.Grants,
	}
	if len(p.Global.Models) == 0 && c.Azure.Model != "" {
		p.Global.Models = []string{c.Azure.Model}
	}
	if p.Defaults.Model == "" {
		p.Defaults.Model = c.Azure.Model
	}
	if p.Defaults.ThinkingLevel == "" {
		p.Defaults.ThinkingLevel = c.Azure.ReasoningEffort
	}
	if c.Azure.ReasoningEffort != "" {
		if len(p.Global.ThinkingLevels) == 0 {
			p.Global.ThinkingLevels = []string{c.Azure.ReasoningEffort}
		}
		if p.ModelThinkingLevels == nil {
			p.ModelThinkingLevels = make(map[string][]string)
		}
		if len(p.ModelThinkingLevels[c.Azure.Model]) == 0 {
			p.ModelThinkingLevels[c.Azure.Model] = []string{c.Azure.ReasoningEffort}
		}
	}
	return p
}

func (c Config) ValidateProvider() error {
	if err := validateBaseURL(c.Azure.BaseURL); err != nil {
		return &Error{Field: "azure.base_url", Cause: err}
	}
	if strings.TrimSpace(c.Azure.APIKey) == "" {
		return invalid("azure.api_key", "is required (set AZURE_OPENAI_API_KEY)")
	}
	if strings.TrimSpace(c.Azure.Model) == "" {
		return invalid("azure.model", "is required (set AZURE_OPENAI_MODEL)")
	}
	return nil
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("must be a valid URL")
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("must be an HTTP(S) URL without userinfo, query, or fragment")
	}
	path := strings.TrimRight(u.Path, "/")
	if path != "" && path != "/openai/v1" {
		return errors.New("must end in /openai/v1")
	}
	return nil
}

func (c R2Config) Enabled() bool {
	return strings.TrimSpace(c.AccessKeyID) != "" && strings.TrimSpace(c.SecretAccessKey) != ""
}

func (c R2Config) Validate() error {
	if (strings.TrimSpace(c.AccessKeyID) == "") != (strings.TrimSpace(c.SecretAccessKey) == "") {
		return invalid("r2", "set both ABOT_R2_ACCESS_KEY_ID and ABOT_R2_SECRET_ACCESS_KEY, or leave both unset")
	}
	if c.Endpoint == "" && c.Bucket == "" && !c.Enabled() {
		return nil
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return invalid("r2.endpoint", "must be an HTTPS origin without credentials, query, or fragment")
	}
	if strings.TrimSpace(c.Bucket) == "" || strings.ContainsAny(c.Bucket, "/\\?#") || strings.TrimSpace(c.Region) == "" {
		return invalid("r2", "bucket and region are required; bucket must not contain path characters")
	}
	if c.PresignExpiry < time.Second || c.PresignExpiry >= 24*time.Hour {
		return invalid("r2.presign_expiry", "must be at least 1s and below the bucket's 24h retention")
	}
	if c.MaxBytes < 1 || c.MaxBytes > 50<<20 {
		return invalid("r2.max_bytes", "must be between 1 byte and 50 MiB")
	}
	return nil
}
