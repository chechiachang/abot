package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/engine"
	"github.com/chechiachang/abot/internal/instructions"
	"github.com/chechiachang/abot/internal/logging"
	"github.com/chechiachang/abot/internal/policy"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
	"github.com/chechiachang/abot/internal/workflow"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var version = "dev"

func NewRootCmd() *cobra.Command {
	v := config.NewViper()
	root := &cobra.Command{
		Use:           "abot",
		Short:         "Telegram assistant and coding agent",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	flags := root.PersistentFlags()
	flags.String("config", "", "config file")
	flags.String("azure-base-url", "", "Azure OpenAI /openai/v1 base URL")
	flags.String("azure-model", "", "Azure OpenAI model or deployment")
	flags.String("azure-reasoning-effort", "", "Azure reasoning effort: none, low, medium, or high")
	flags.String("azure-timeout", "", "Azure request timeout")
	flags.Int("azure-max-retries", -1, "maximum retries for transient Azure errors (0-5)")
	flags.String("policy-file", "", "administrator-managed policy JSON file")
	flags.String("audit-file", "", "administrator audit JSONL file")
	flags.String("state-db", "", "SQLite path for ADK sessions and runtime state")
	flags.String("turn-timeout", "", "maximum duration for one agent turn")
	flags.String("branch", "main", "conversation branch ID")
	flags.String("system-file", "", "system instructions file")
	flags.String("soul-file", "", "optional persona file")
	flags.String("log-level", "", "log level")
	flags.String("coding-workdir", "", "repository workdir for coding tools")
	flags.String("coding-command-timeout", "", "maximum duration for a coding shell command")
	flags.Int64("coding-max-output-bytes", 0, "maximum coding-tool output bytes")
	for key, name := range map[string]string{
		"config":                   "config",
		"azure.base_url":           "azure-base-url",
		"azure.model":              "azure-model",
		"azure.reasoning_effort":   "azure-reasoning-effort",
		"azure.timeout":            "azure-timeout",
		"azure.max_retries":        "azure-max-retries",
		"policy_file":              "policy-file",
		"audit_file":               "audit-file",
		"runtime.sqlite_path":      "state-db",
		"runtime.turn_timeout":     "turn-timeout",
		"instructions.system_file": "system-file",
		"instructions.soul_file":   "soul-file",
		"log.level":                "log-level",
		"coding.workdir":           "coding-workdir",
		"coding.command_timeout":   "coding-command-timeout",
		"coding.max_output_bytes":  "coding-max-output-bytes",
	} {
		_ = v.BindPFlag(key, flags.Lookup(name))
	}

	root.AddCommand(versionCommand(), configCommand(v), chatCommand(v), policyCommand(v), providerCommand(v), modelsCommand(v), capabilitiesCommand(v), grantsCommand(v), chatsCommand(v), diagnosticsCommand(v), stateCommand(v), skillsCommand(v), accessCommand(v), telegramCommand(v), mcpCommand(v), urlCommand(v), quoteCommand(), polymarketCommand(), healthCommand(v))
	return root
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, _ []string) { cmd.Println(version) },
	}
}

func configCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "Configuration commands"}
	command.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate configuration without contacting the provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			if err := cfg.ValidateProvider(); err != nil {
				return err
			}
			if _, _, err := configuredPolicy(cfg); err != nil {
				return err
			}
			logger := logging.New(cfg.Azure.APIKey, cfg.Logfire.Token, cfg.URLReader.FirecrawlAPIKey, cfg.Telegram.BotToken, cfg.Telegram.MorselAPIKey)
			level, _ := logrus.ParseLevel(cfg.Log.Level)
			logger.SetLevel(level)
			logger.Debug("configuration validated")
			if _, err := instructions.Load(cfg.Instructions.SystemFile, cfg.Instructions.SoulFile); err != nil {
				return err
			}
			cmd.Println("configuration valid")
			return nil
		},
	})
	return command
}

func chatCommand(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "chat <prompt>",
		Short: "Send a prompt to the ADK coordinator",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			if err := cfg.ValidateProvider(); err != nil {
				return err
			}
			currentPolicy, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			if _, err := currentPolicy.Effective(0); err != nil {
				return err
			}
			logger := logging.New(cfg.Azure.APIKey, cfg.Logfire.Token, cfg.URLReader.FirecrawlAPIKey, cfg.Telegram.BotToken, cfg.Telegram.MorselAPIKey)
			level, _ := logrus.ParseLevel(cfg.Log.Level)
			logger.SetLevel(level)
			stopTracing := configureTracing(cmd.Context(), cfg, logger)
			defer stopTracing()
			chatID := "local-session"
			branchID, _ := cmd.Root().PersistentFlags().GetString("branch")
			if strings.TrimSpace(branchID) == "" {
				return errors.New("branch ID cannot be empty")
			}
			logger.WithFields(logrus.Fields{"provider": "azure-openai", "chat_id": chatID}).Debug("starting chat")
			state, err := agentruntime.Open(cfg.Runtime.SQLitePath)
			if err != nil {
				return err
			}
			defer state.Close()
			host := engine.Engine{
				Config: cfg, State: state, Turns: agentruntime.NewTurnManager(), Logger: logger,
				LoadPolicy: func() (policy.Policy, error) {
					current, _, err := configuredPolicy(cfg)
					return current, err
				},
			}
			wrote, outputErr := false, error(nil)
			_, err = host.Run(cmd.Context(), chatID, 0, "local-user", branchID, strings.Join(args, " "), func(update workflow.Event) {
				if update.Agent != "coordinator" || update.Text == "" || outputErr != nil {
					return
				}
				if update.Partial {
					_, outputErr = fmt.Fprint(cmd.OutOrStdout(), update.Text)
					wrote = true
				} else if update.Kind == workflow.EventResult && !wrote {
					_, outputErr = fmt.Fprint(cmd.OutOrStdout(), update.Text)
					wrote = true
				}
			})
			if err != nil {
				return err
			}
			if outputErr != nil {
				return outputErr
			}
			if !wrote {
				return errors.New("agent completed without a text response")
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout())
			return err
		},
	}
}
