package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"slices"
	"strconv"
	"strings"

	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/policy"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
	"github.com/chechiachang/abot/internal/skills"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

func configuredPolicy(cfg config.Config) (policy.Policy, policy.Store, error) {
	store := policy.Store{PolicyPath: cfg.PolicyFile, AuditPath: cfg.AuditFile}
	if current, exists, err := store.Load(); err != nil {
		return policy.Policy{}, store, err
	} else if exists {
		return current, store, nil
	}
	current := cfg.EffectivePolicy()
	if err := current.Validate(); err != nil {
		return policy.Policy{}, store, err
	}
	return current, store, nil
}

func filterTools(tools []tool.Tool, allowed []string) []tool.Tool {
	filtered := make([]tool.Tool, 0, len(tools))
	for _, item := range tools {
		if slices.Contains(allowed, item.Name()) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func policyCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "policy", Short: "Validate and administer local policy"}
	command.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate active policy without contacting the provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			cmd.Printf("policy valid: %s\n", current.Summary())
			return nil
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "export <file>",
		Short: "Export active policy as an editable JSON proposal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(current, "", "  ")
			if err != nil {
				return err
			}
			file, err := os.OpenFile(args[0], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return fmt.Errorf("create policy proposal: %w", err)
			}
			if _, err = file.Write(append(data, '\n')); err != nil {
				file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
			cmd.Printf("exported %s\n", args[0])
			return nil
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "review <file>",
		Short: "Validate and summarize a policy proposal without applying it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			candidate, err := policy.LoadFile(args[0])
			if err != nil {
				return err
			}
			cmd.Printf("before: %s\nafter:  %s\n", current.Summary(), candidate.Summary())
			return nil
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "apply <file>",
		Short: "Apply a reviewed policy file and write a redacted audit record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			candidate, err := policy.LoadFile(args[0])
			if err != nil {
				return err
			}
			_, store, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			actor := "local"
			if current, err := user.Current(); err == nil {
				actor = current.Username
			}
			if err := store.Apply(candidate, actor); err != nil {
				return err
			}
			cmd.Printf("policy applied: %s\n", candidate.Summary())
			return nil
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "audit",
		Short: "Show local redacted policy audit records",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			file, err := os.Open(cfg.AuditFile)
			if errors.Is(err, os.ErrNotExist) {
				cmd.Println("no audit records")
				return nil
			}
			if err != nil {
				return err
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 4096), 1<<20)
			for scanner.Scan() {
				var event policy.AuditEvent
				if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
					return fmt.Errorf("decode audit record: %w", err)
				}
				cmd.Printf("%s %s actor=%s before={%s} after={%s}\n", event.At.UTC().Format(timeFormat), event.Action, event.Actor, event.Before, event.After)
			}
			return scanner.Err()
		},
	})
	return command
}

const timeFormat = "2006-01-02T15:04:05Z"

func providerCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "provider", Short: "Inspect configured LLM providers"}
	command.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show Azure OpenAI configuration without credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			key := "missing"
			if cfg.Azure.APIKey != "" {
				key = "configured"
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			selected, err := current.Effective(0)
			if err != nil {
				return err
			}
			cmd.Printf("provider=azure-openai base_url=%s model=%s reasoning_effort=%s max_retries=%d api_key=%s\n", cfg.Azure.BaseURL, selected.Model, valueOrUnset(selected.ThinkingLevel), cfg.Azure.MaxRetries, key)
			cmd.Println("OAuth login is not supported for Azure API keys.")
			return nil
		},
	})
	return command
}

func modelsCommand(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List configured and policy-allowed models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			for _, model := range current.Global.Models {
				marker := " "
				if model == current.Defaults.Model {
					marker = "*"
				}
				cmd.Printf("%s %s\n", marker, model)
			}
			return nil
		},
	}
}

func capabilitiesCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "capabilities", Short: "Inspect global and effective chat capabilities"}
	show := &cobra.Command{
		Use:   "show",
		Short: "Show capabilities after global limits are applied",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			chatID, _ := cmd.Flags().GetInt64("chat-id")
			selected, err := current.Effective(chatID)
			if err != nil {
				return err
			}
			cmd.Printf("model=%s thinking=%s tools=%s skills=%s mcp=%s\n", selected.Model, valueOrUnset(selected.ThinkingLevel), strings.Join(selected.Tools, ","), strings.Join(selected.Skills, ","), strings.Join(selected.MCP, ","))
			return nil
		},
	}
	show.Flags().Int64("chat-id", 0, "optional Telegram chat ID")
	command.AddCommand(show)
	return command
}

func grantsCommand(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "grants",
		Short: "List persisted private-user, group, and channel grants",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			for _, id := range current.Telegram.ApprovedChannelIDs {
				cmd.Printf("channel %d (configured)\n", id)
			}
			for _, grant := range current.Grants {
				cmd.Printf("%s %d granted_by=%s granted_at=%s\n", grant.Kind, grant.ID, grant.GrantedBy, grant.GrantedAt)
			}
			if len(current.Telegram.ApprovedChannelIDs)+len(current.Grants) == 0 {
				cmd.Println("no grants; edit a proposal with 'abot policy export', then review and apply it")
			}
			return nil
		},
	}
}

func chatsCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "chats", Short: "Inspect chats with configured policy overrides"}
	for _, use := range []string{"list", "search <query>"} {
		args := cobra.NoArgs
		if strings.HasPrefix(use, "search") {
			args = cobra.ExactArgs(1)
		}
		chatCmd := &cobra.Command{
			Use:   use,
			Short: "List or search configured chat overrides",
			Args:  args,
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := config.Load(v)
				if err != nil {
					return err
				}
				current, _, err := configuredPolicy(cfg)
				if err != nil {
					return err
				}
				query := ""
				if len(args) > 0 {
					query = args[0]
				}
				ids := make(map[string]bool, len(current.ChatOverrides))
				for id := range current.ChatOverrides {
					if query == "" || strings.Contains(id, query) {
						ids[id] = true
					}
				}
				userIDs := []string{"local-user"}
				for _, id := range current.Telegram.AllowedUserIDs {
					userIDs = append(userIDs, strconv.FormatInt(id, 10))
				}
				if userID, _ := cmd.Flags().GetString("user-id"); userID != "" {
					userIDs = []string{userID}
				}
				state, err := agentruntime.OpenReadOnly(cfg.Runtime.SQLitePath)
				if err == nil {
					defer state.Close()
					for _, userID := range slices.Compact(userIDs) {
						response, err := state.Sessions().List(cmd.Context(), &session.ListRequest{AppName: "abot", UserID: userID})
						if err != nil {
							return err
						}
						for _, chat := range response.Sessions {
							if query == "" || strings.Contains(chat.ID(), query) {
								cmd.Printf("session=%s user=%s events=%d updated=%s\n", chat.ID(), chat.UserID(), chat.Events().Len(), chat.LastUpdateTime().UTC().Format(timeFormat))
								ids[chat.ID()] = true
							}
						}
					}
				} else if !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				ordered := make([]string, 0, len(ids))
				for id := range ids {
					ordered = append(ordered, id)
				}
				slices.Sort(ordered)
				for _, id := range ordered {
					cmd.Println("policy_override=" + id)
				}
				if len(ordered) == 0 {
					cmd.Println("no matching chats")
				}
				return nil
			},
		}
		chatCmd.Flags().String("user-id", "", "inspect one ADK session user ID")
		command.AddCommand(chatCmd)
	}
	importCmd := &cobra.Command{
		Use:   "import <file.jsonl>",
		Short: "Validate a versioned chat-history JSONL file; use --apply to import",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			info, err := os.Stat(args[0])
			if err != nil {
				return err
			}
			if info.Size() <= 0 || info.Size() > 64<<20 {
				return errors.New("import file must be between 1 byte and 64 MiB")
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			apply, _ := cmd.Flags().GetBool("apply")
			if !apply {
				if _, err := os.Stat(cfg.Runtime.SQLitePath); errors.Is(err, fs.ErrNotExist) {
					report, err := agentruntime.ValidateJSONL(data, args[0])
					if err != nil {
						return err
					}
					cmd.Printf("source=%s dry_run=true imported=%d skipped=%d failed=%d\n", report.Source, report.Imported, report.Skipped, report.Failed)
					if report.Failed > 0 {
						return errors.New("import validation failed; no history was imported")
					}
					return nil
				}
			}
			var state *agentruntime.Store
			if apply {
				state, err = agentruntime.Open(cfg.Runtime.SQLitePath)
			} else {
				state, err = agentruntime.OpenReadOnly(cfg.Runtime.SQLitePath)
			}
			if err != nil {
				return err
			}
			defer state.Close()
			report, err := state.ImportJSONL(cmd.Context(), data, args[0], apply)
			if err != nil {
				return err
			}
			cmd.Printf("source=%s dry_run=%t imported=%d skipped=%d failed=%d\n", report.Source, report.DryRun, report.Imported, report.Skipped, report.Failed)
			if report.Failed > 0 {
				return errors.New("import validation failed; no history was imported")
			}
			return nil
		},
	}
	importCmd.Flags().Bool("apply", false, "write imported events and a report to SQLite")
	command.AddCommand(importCmd)
	forkCmd := &cobra.Command{
		Use:   "fork <chat-id> <parent-branch> <new-branch> [checkpoint-event-id]",
		Short: "Fork a durable conversation at a completed event checkpoint",
		Args:  cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			userID, _ := cmd.Flags().GetString("user-id")
			store, err := agentruntime.Open(cfg.Runtime.SQLitePath)
			if err != nil {
				return err
			}
			defer store.Close()
			checkpoint := ""
			if len(args) == 4 {
				checkpoint = args[3]
			}
			branch, err := store.ForkBranch(cmd.Context(), args[0], userID, args[1], args[2], checkpoint)
			if err != nil {
				return err
			}
			cmd.Printf("forked chat=%s branch=%s parent=%s checkpoint=%s session=%s\n", branch.ChatID, branch.BranchID, branch.ParentBranchID, branch.CheckpointEventID, branch.SessionID)
			return nil
		},
	}
	forkCmd.Flags().String("user-id", "local-user", "ADK session user ID")
	command.AddCommand(forkCmd)
	compactCmd := &cobra.Command{
		Use:   "compact <chat-id> <branch-id> <summary>",
		Short: "Archive branch history and start a compacted session from a summary",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			userID, _ := cmd.Flags().GetString("user-id")
			store, err := agentruntime.Open(cfg.Runtime.SQLitePath)
			if err != nil {
				return err
			}
			defer store.Close()
			branch, err := store.CompactBranch(cmd.Context(), args[0], userID, args[1], args[2])
			if err != nil {
				return err
			}
			cmd.Printf("compacted chat=%s branch=%s checkpoint=%s session=%s\n", branch.ChatID, branch.BranchID, branch.CheckpointEventID, branch.SessionID)
			return nil
		},
	}
	compactCmd.Flags().String("user-id", "local-user", "ADK session user ID")
	command.AddCommand(compactCmd)
	return command
}

func diagnosticsCommand(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "diagnostics",
		Short: "Check local configuration and readiness without model calls",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			catalog, err := skills.Discover(cfg.Skills.Paths)
			if err != nil {
				return err
			}
			selected, err := current.Effective(0)
			if err != nil {
				return err
			}
			if _, err := catalog.LoadSelected(selected.Skills); err != nil {
				return err
			}
			key := "configured"
			if cfg.Azure.APIKey == "" {
				key = "missing (set AZURE_OPENAI_API_KEY)"
			}
			users := len(current.Telegram.AllowedUserIDs)
			cmd.Printf("configuration=valid policy=valid azure_api_key=%s allowed_users=%d default_group=%d model=%s\n", key, users, current.Telegram.DefaultGroupID, current.Defaults.Model)
			if users == 0 {
				cmd.Println("telegram authorization denies all agent requests until telegram.allowed_user_ids is configured")
			}
			return nil
		},
	}
}

func skillsCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "skills", Short: "Discover and validate Agent Skills"}
	command.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List discovered skills; skills do not grant tools",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			catalog, err := skills.Discover(cfg.Skills.Paths)
			if err != nil {
				return err
			}
			for _, skill := range catalog.List() {
				cmd.Printf("%s\t%s\t%s\n", skill.Name, skill.Description, skill.Path)
			}
			return nil
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate selected skills and discovery paths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			selection, err := current.Effective(0)
			if err != nil {
				return err
			}
			catalog, err := skills.Discover(cfg.Skills.Paths)
			if err != nil {
				return err
			}
			selected, err := catalog.LoadSelected(selection.Skills)
			if err != nil {
				return err
			}
			cmd.Printf("skills valid: selected=%d discovered=%d\n", len(selected), len(catalog.List()))
			return nil
		},
	})
	return command
}

func valueOrUnset(value string) string {
	if value == "" {
		return "unset"
	}
	return value
}
