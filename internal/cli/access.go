package cli

import (
	"fmt"
	"os/user"

	"github.com/chechiachang/abot/internal/config"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func accessCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "access", Short: "Review Telegram access requests"}
	command.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List pending Telegram access requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			store, err := agentruntime.OpenReadOnly(cfg.Runtime.SQLitePath)
			if err != nil {
				return err
			}
			defer store.Close()
			requests, err := store.ListAccessRequests(cmd.Context(), "pending")
			if err != nil {
				return err
			}
			for _, request := range requests {
				cmd.Printf("request=%s chat=%d user=%d chat_type=%s status=%s created=%s\n", request.ID, request.ChatID, request.UserID, request.ChatType, request.Status, request.CreatedAt.UTC().Format(timeFormat))
			}
			if len(requests) == 0 {
				cmd.Println("no pending access requests")
			}
			return nil
		},
	})
	for _, action := range []struct {
		name    string
		approve bool
	}{{"approve", true}, {"reject", false}} {
		action := action
		command.AddCommand(&cobra.Command{
			Use:   action.name + " <request-id>",
			Short: fmt.Sprintf("%s one pending Telegram access request", action.name),
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := config.Load(v)
				if err != nil {
					return err
				}
				store, err := agentruntime.OpenAdmin(cfg.Runtime.SQLitePath)
				if err != nil {
					return err
				}
				defer store.Close()
				currentUser, err := user.Current()
				if err != nil {
					return fmt.Errorf("identify local administrator: %w", err)
				}
				if err := store.ReviewAccessRequest(cmd.Context(), args[0], currentUser.Username, action.approve); err != nil {
					return err
				}
				if action.approve {
					cmd.Println("request approved; add the sender to telegram.allowed_user_ids before agent requests are permitted")
				} else {
					cmd.Println("request rejected")
				}
				return nil
			},
		})
	}
	return command
}
