package cli

import (
	"fmt"

	"github.com/chechiachang/abot/internal/quotes"
	"github.com/spf13/cobra"
)

func quoteCommand() *cobra.Command {
	command := &cobra.Command{Use: "quote", Short: "Read public market and currency quotes"}
	command.AddCommand(&cobra.Command{
		Use:   "read <ticker|MAX:pair|FX:BASE/QUOTE|BOT:CURRENCY>",
		Short: "Read a bounded public quote",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := quotes.NewClient()
			quote, err := client.Read(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), quote.String())
			return err
		},
	})
	return command
}
