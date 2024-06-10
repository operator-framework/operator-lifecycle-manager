package registry

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/lib/registry"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func newRegistryDeprecateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Hidden: true,
		Use:    "deprecatetruncate",
		Short:  "deprecate operator bundle from registry DB",
		Long: `deprecate operator bundle from registry DB

` + sqlite.DeprecationMessage,

		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},

		RunE: deprecateFunc,
		Args: cobra.NoArgs,
	}

	cmd.Flags().Bool("debug", false, "enable debug logging")
	cmd.Flags().StringP("database", "d", "index.db", "relative path to database file")
	cmd.Flags().StringSliceP("bundle-images", "b", []string{}, "comma separated list of links to bundle image")
	cmd.Flags().Bool("permissive", false, "allow registry load errors")
	cmd.Flags().Bool("allow-package-removal", false, "removes the entire package if the heads of all channels in the package are deprecated")

	return cmd
}

func deprecateFunc(cmd *cobra.Command, _ []string) error {
	permissive, err := cmd.Flags().GetBool("permissive")
	if err != nil {
		return err
	}
	fromFilename, err := cmd.Flags().GetString("database")
	if err != nil {
		return err
	}
	bundleImages, err := cmd.Flags().GetStringSlice("bundle-images")
	if err != nil {
		return err
	}
	allowPackageRemoval, err := cmd.Flags().GetBool("allow-package-removal")
	if err != nil {
		return err
	}

	request := registry.DeprecateFromRegistryRequest{
		Permissive:          permissive,
		InputDatabase:       fromFilename,
		Bundles:             bundleImages,
		AllowPackageRemoval: allowPackageRemoval,
	}

	logger := logrus.WithFields(logrus.Fields{"bundles": bundleImages})

	logger.Info("deprecating from registry")

	registryDeprecator := registry.NewRegistryDeprecator(logger)

	err = registryDeprecator.DeprecateFromRegistry(request)
	if err != nil {
		logger.Fatal(err)
	}
	return nil
}
