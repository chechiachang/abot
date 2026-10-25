package cli

import (
	"encoding/json"
	"strings"

	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/logging"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/adk/session"
)

func stateCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "state", Short: "Read-only SQLite conversation and task inspector"}
	command.AddCommand(&cobra.Command{
		Use:   "branches <chat-id>",
		Short: "List durable branches and checkpoints",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, err := readonlyStore(v)
			if err != nil {
				return err
			}
			defer store.Close()
			branches, err := store.ListBranches(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, branch := range branches {
				cmd.Printf("branch=%s parent=%s checkpoint=%s session=%s created=%s\n", branch.BranchID, branch.ParentBranchID, branch.CheckpointEventID, branch.SessionID, branch.CreatedAt.UTC().Format(timeFormat))
				revisions, err := store.ListBranchRevisions(cmd.Context(), args[0], branch.BranchID)
				if err != nil {
					return err
				}
				for _, revision := range revisions {
					cmd.Printf("revision=%s reason=%s checkpoint=%s session=%s created=%s\n", revision.ID, revision.Reason, revision.CheckpointEventID, revision.SessionID, revision.CreatedAt.UTC().Format(timeFormat))
				}
			}
			return nil
		},
	})
	listTasks := &cobra.Command{
		Use:   "tasks",
		Short: "List durable generations and their terminal status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := readonlyStore(v)
			if err != nil {
				return err
			}
			defer store.Close()
			chatID, _ := cmd.Flags().GetString("chat-id")
			tasks, err := store.ListTasks(cmd.Context(), chatID, 100)
			if err != nil {
				return err
			}
			for _, task := range tasks {
				cmd.Printf("generation=%s chat=%s branch=%s status=%s started=%s result_event=%s error_kind=%s\n", task.ID, task.ChatID, task.BranchID, task.Status, task.StartedAt.UTC().Format(timeFormat), task.ResultEventID, task.ErrorKind)
			}
			return nil
		},
	}
	listTasks.Flags().String("chat-id", "", "filter by chat ID")
	command.AddCommand(listTasks)
	listInputs := &cobra.Command{
		Use:   "inputs",
		Short: "Inspect durable steering and follow-up queue status without showing message text",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := readonlyStore(v)
			if err != nil {
				return err
			}
			defer store.Close()
			chatID, _ := cmd.Flags().GetString("chat-id")
			inputs, err := store.ListInputs(cmd.Context(), chatID, 100)
			if err != nil {
				return err
			}
			for _, input := range inputs {
				cmd.Printf("input=%s chat=%s branch=%s kind=%s status=%s bytes=%d error_kind=%s created=%s\n", input.ID, input.ChatID, input.BranchID, input.Kind, input.Status, len(input.Text), input.ErrorKind, input.CreatedAt.UTC().Format(timeFormat))
			}
			return nil
		},
	}
	listInputs.Flags().String("chat-id", "", "filter by chat ID")
	command.AddCommand(listInputs)
	command.AddCommand(&cobra.Command{
		Use:   "task <generation-id>",
		Short: "Show one generation and its tool/progress records",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, cfg, err := readonlyStore(v)
			if err != nil {
				return err
			}
			defer store.Close()
			task, records, steps, err := store.TaskDetails(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			cmd.Printf("generation=%s chat=%s branch=%s status=%s result_event=%s error_kind=%s\n", task.ID, task.ChatID, task.BranchID, task.Status, task.ResultEventID, task.ErrorKind)
			for _, record := range records {
				cmd.Printf("record=%s kind=%s name=%s status=%s error_kind=%s\n", record.ID, record.Kind, logging.Redact(record.Name, cfg.Azure.APIKey), record.Status, record.ErrorKind)
			}
			for _, step := range steps {
				cmd.Printf("step=%s status=%s title=%s\n", step.StepID, step.Status, logging.Redact(step.Title, cfg.Azure.APIKey))
			}
			return nil
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "progress <chat-id> <branch-id>",
		Short: "Show the latest structured progress for a branch",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, cfg, err := readonlyStore(v)
			if err != nil {
				return err
			}
			defer store.Close()
			steps, err := store.BranchProgress(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			for _, step := range steps {
				cmd.Printf("step=%s status=%s title=%s\n", step.StepID, step.Status, logging.Redact(step.Title, cfg.Azure.APIKey))
			}
			return nil
		},
	})
	events := &cobra.Command{
		Use:   "events <session-id>",
		Short: "List persisted ADK events; use --raw to include redacted text/details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, cfg, err := readonlyStore(v)
			if err != nil {
				return err
			}
			defer store.Close()
			userID, _ := cmd.Flags().GetString("user-id")
			limit, _ := cmd.Flags().GetInt("limit")
			raw, _ := cmd.Flags().GetBool("raw")
			resp, err := store.Sessions().Get(cmd.Context(), &session.GetRequest{AppName: "abot", UserID: userID, SessionID: args[0], NumRecentEvents: limit})
			if err != nil {
				return err
			}
			for event := range resp.Session.Events().All() {
				if event == nil {
					continue
				}
				cmd.Printf("event=%s time=%s author=%s branch=%s invocation=%s partial=%t", event.ID, event.Timestamp.UTC().Format(timeFormat), event.Author, event.Branch, event.InvocationID, event.Partial)
				if raw {
					if body := eventText(event); body != "" {
						cmd.Printf(" text=%s", logging.Redact(body, cfg.Azure.APIKey))
					}
					if details := eventDetails(event); len(details) > 0 {
						encoded, _ := json.Marshal(details)
						cmd.Printf(" details=%s", logging.Redact(string(encoded), cfg.Azure.APIKey))
					}
				}
				cmd.Println()
			}
			return nil
		},
	}
	events.Flags().String("user-id", "local-user", "ADK session user ID")
	events.Flags().Int("limit", 0, "maximum recent events (0 means all)")
	events.Flags().Bool("raw", false, "include redacted event text and tool details")
	command.AddCommand(events)
	return command
}

func readonlyStore(v *viper.Viper) (*agentruntime.Store, config.Config, error) {
	cfg, err := config.Load(v)
	if err != nil {
		return nil, config.Config{}, err
	}
	store, err := agentruntime.OpenReadOnly(cfg.Runtime.SQLitePath)
	if err != nil {
		return nil, config.Config{}, err
	}
	return store, cfg, nil
}

func eventText(event *session.Event) string {
	if event.Content == nil {
		return ""
	}
	var parts []string
	for _, part := range event.Content.Parts {
		if part != nil && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func eventDetails(event *session.Event) map[string]any {
	if event.Content == nil {
		return nil
	}
	details := make(map[string]any)
	for _, part := range event.Content.Parts {
		if part == nil {
			continue
		}
		if call := part.FunctionCall; call != nil {
			details["tool_call"] = map[string]any{"name": call.Name, "args": redactDetails(call.Args)}
		}
		if response := part.FunctionResponse; response != nil {
			details["tool_response"] = map[string]any{"name": response.Name, "response": redactDetails(response.Response)}
		}
	}
	return details
}

func redactDetails(value any) any {
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			normalized := strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(key))
			if strings.Contains(normalized, "api_key") || strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") || strings.Contains(normalized, "credential") || strings.Contains(normalized, "authorization") {
				out[key] = "[REDACTED]"
			} else {
				out[key] = redactDetails(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = redactDetails(item)
		}
		return out
	case string:
		return logging.Redact(value)
	default:
		return value
	}
}
