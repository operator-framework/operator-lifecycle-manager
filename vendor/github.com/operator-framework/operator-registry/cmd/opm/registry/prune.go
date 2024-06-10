package registry

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/lib/registry"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func newRegistryPruneCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "prune",
		Short: "prune an operator registry DB of all but specified packages",
		Long: `prune an operator registry DB of all but specified packages

` + sqlite.DeprecationMessage,

		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},

		RunE: runRegistryPruneCmdFunc,
		Args: cobra.NoArgs,
	}

	rootCmd.Flags().Bool("debug", false, "enable debug logging")
	rootCmd.Flags().StringP("database", "d", "bundles.db", "relative path to database file")
	rootCmd.Flags().StringSliceP("packages", "p", []string{}, "comma separated list of package names to be kept")
	if err := rootCmd.MarkFlagRequired("packages"); err != nil {
		logrus.Panic("Failed to set required `packages` flag for `registry rm`")
	}
	rootCmd.Flags().Bool("permissive", false, "allow registry load errors")

	return rootCmd
}

func runRegistryPruneCmdFunc(cmd *cobra.Command, _ []string) error {
	fromFilename, err := cmd.Flags().GetString("database")
	if err != nil {
		return err
	}
	packages, err := cmd.Flags().GetStringSlice("packages")
	if err != nil {
		return err
	}
	permissive, err := cmd.Flags().GetBool("permissive")
	if err != nil {
		return err
	}

	request := registry.PruneFromRegistryRequest{
		Packages:      packages,
		InputDatabase: fromFilename,
		Permissive:    permissive,
	}

	logger := logrus.WithFields(logrus.Fields{"packages": packages})

	logger.Info("pruning from the registry")

	registryPruner := registry.NewRegistryPruner(logger)

	err = registryPruner.PruneFromRegistry(request)
	if err != nil {
		return err
	}

	return nil
}
