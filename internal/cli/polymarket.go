package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chechiachang/abot/internal/polymarket"
	"github.com/spf13/cobra"
)

func polymarketCommand() *cobra.Command {
	command := &cobra.Command{Use: "polymarket", Short: "Read public Polymarket markets"}
	command.AddCommand(&cobra.Command{
		Use: "search <query>", Short: "Search active public markets", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			markets, err := polymarket.NewClient().Search(cmd.Context(), args[0], 5)
			if err != nil {
				return err
			}
			for _, market := range markets {
				cmd.Printf("%s\n%s\n\n", market.Question, market.URL())
			}
			if len(markets) == 0 {
				cmd.Println("No matching active markets.")
			}
			return nil
		},
	})
	watch := &cobra.Command{Use: "watch <asset-id> [asset-id...]", Short: "Subscribe to public market updates", Args: cobra.MinimumNArgs(1)}
	watch.Flags().Duration("duration", 0, "stop after this duration (default: until interrupted)")
	watch.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if duration, _ := watch.Flags().GetDuration("duration"); duration > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, duration)
			defer cancel()
		}
		return polymarket.NewClient().Watch(ctx, args, func(event string) {
			var compact json.RawMessage
			if json.Unmarshal([]byte(event), &compact) == nil {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(compact))
			}
		})
	}
	command.AddCommand(watch)
	return command
}
