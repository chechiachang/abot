package cli

import (
	"slices"

	"github.com/chechiachang/abot/internal/config"
	mcpconnections "github.com/chechiachang/abot/internal/integrations/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func mcpCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "mcp", Short: "Inspect administrator-configured MCP connections"}
	command.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured MCP connections without exposing credentials",
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
			for _, connection := range cfg.MCP.Connections {
				cmd.Printf("%s\t%s\tglobally_allowed=%t\tdefault_selected=%t\n", connection.Name, connection.Transport, slices.Contains(current.Global.MCP, connection.Name), slices.Contains(selection.MCP, connection.Name))
			}
			if len(cfg.MCP.Connections) == 0 {
				cmd.Println("no MCP connections configured")
			}
			return nil
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate MCP connection and capability configuration without connecting",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			cmd.Printf("MCP configuration valid: connections=%d\n", len(cfg.MCP.Connections))
			return nil
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "probe <connection>",
		Short: "Connect and discover stable names for one configured server's tools",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			manager := mcpconnections.NewManager(cfg.MCP.Connections)
			tools, closer, err := manager.Probe(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			defer closer.Close()
			for _, item := range tools {
				cmd.Printf("%s\n", item.Name())
			}
			if len(tools) == 0 {
				cmd.Println("server advertised no callable tools")
			}
			return nil
		},
	})
	return command
}
