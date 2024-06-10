package registry

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/lib/registry"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func newRegistryRmCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "rm",
		Short: "remove operator from operator registry DB",
		Long: `Remove operator from operator registry DB

` + sqlite.DeprecationMessage,

		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},

		RunE: rmFunc,
		Args: cobra.NoArgs,
	}

	rootCmd.Flags().Bool("debug", false, "enable debug logging")
	rootCmd.Flags().StringP("database", "d", "bundles.db", "relative path to database file")
	rootCmd.Flags().StringSliceP("packages", "o", nil, "comma separated list of package names to be deleted")
	if err := rootCmd.MarkFlagRequired("packages"); err != nil {
		logrus.Panic("Failed to set required `packages` flag for `registry rm`")
	}
	rootCmd.Flags().Bool("permissive", false, "allow registry load errors")

	return rootCmd
}

func rmFunc(cmd *cobra.Command, _ []string) error {
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

	request := registry.DeleteFromRegistryRequest{
		Packages:      packages,
		InputDatabase: fromFilename,
		Permissive:    permissive,
	}

	logger := logrus.WithFields(logrus.Fields{"packages": packages})

	logger.Info("removing from the registry")

	registryDeleter := registry.NewRegistryDeleter(logger)

	err = registryDeleter.DeleteFromRegistry(request)
	if err != nil {
		return err
	}

	return nil
}
