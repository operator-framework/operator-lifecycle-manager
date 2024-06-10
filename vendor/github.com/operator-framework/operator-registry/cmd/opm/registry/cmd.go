package registry

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

// NewOpmRegistryCmd returns the appregistry-server command
func NewOpmRegistryCmd(showAlphaHelp bool) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "registry",
		Short: "interact with operator-registry database",
		Long: `interact with operator-registry database building, modifying and/or serving the operator-registry database

` + sqlite.DeprecationMessage,
		PersistentPreRun: func(_ *cobra.Command, _ []string) {
			sqlite.LogSqliteDeprecation()
		},
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},
		Args: cobra.NoArgs,
	}

	rootCmd.AddCommand(newRegistryServeCmd())
	rootCmd.AddCommand(newRegistryAddCmd(showAlphaHelp))
	rootCmd.AddCommand(newRegistryRmCmd())
	rootCmd.AddCommand(newRegistryPruneCmd())
	rootCmd.AddCommand(newRegistryPruneStrandedCmd())
	rootCmd.AddCommand(newRegistryDeprecateCmd())

	return rootCmd
}
