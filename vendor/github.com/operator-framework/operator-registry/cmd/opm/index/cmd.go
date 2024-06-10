package index

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

// AddCommand adds the index subcommand to the given parent command.
func AddCommand(parent *cobra.Command, showAlphaHelp bool) {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "generate operator index container images",
		Long: `generate operator index container images from preexisting operator bundles

` + sqlite.DeprecationMessage,

		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			sqlite.LogSqliteDeprecation()
			if skipTLS, err := cmd.Flags().GetBool("skip-tls"); err == nil && skipTLS {
				logrus.Warn("--skip-tls flag is set: this mode is insecure and meant for development purposes only.")
			}
		},
		Args: cobra.NoArgs,
	}

	parent.AddCommand(cmd)

	cmd.AddCommand(newIndexDeleteCmd())
	addIndexAddCmd(cmd, showAlphaHelp)
	cmd.AddCommand(newIndexExportCmd())
	cmd.AddCommand(newIndexPruneCmd())
	cmd.AddCommand(newIndexDeprecateTruncateCmd())
	cmd.AddCommand(newIndexPruneStrandedCmd())
}
