package registry

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/lib/registry"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func newRegistryPruneStrandedCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "prune-stranded",
		Short: "prune an operator registry DB of stranded bundles",
		Long: `prune an operator registry DB of stranded bundles - bundles that are not associated with a particular package

` + sqlite.DeprecationMessage,

		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},

		RunE: runRegistryPruneStrandedCmdFunc,
		Args: cobra.NoArgs,
	}

	rootCmd.Flags().Bool("debug", false, "enable debug logging")
	rootCmd.Flags().StringP("database", "d", "bundles.db", "relative path to database file")

	return rootCmd
}

func runRegistryPruneStrandedCmdFunc(cmd *cobra.Command, _ []string) error {
	fromFilename, err := cmd.Flags().GetString("database")
	if err != nil {
		return err
	}

	request := registry.PruneStrandedFromRegistryRequest{
		InputDatabase: fromFilename,
	}

	logger := logrus.WithFields(logrus.Fields{})

	logger.Info("pruning from the registry")

	registryStrandedPruner := registry.NewRegistryStrandedPruner(logger)

	err = registryStrandedPruner.PruneStrandedFromRegistry(request)
	if err != nil {
		return err
	}

	return nil
}
