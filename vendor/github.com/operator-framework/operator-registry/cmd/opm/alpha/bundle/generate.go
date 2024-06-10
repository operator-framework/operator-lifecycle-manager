package bundle

import (
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
)

// newBundleGenerateCmd returns a command that will generate operator bundle
// annotations.yaml metadata
func newBundleGenerateCmd() *cobra.Command {
	bundleGenerateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate operator bundle metadata and Dockerfile",
		Long: `The "opm alpha bundle generate" command will generate operator
bundle metadata if needed and a Dockerfile to build Operator bundle image.

$ opm alpha bundle generate --directory /test/0.1.0/ --package test-operator \
	--channels stable,beta --default stable

Note:
* All manifests yaml must be in the same directory.`,
		RunE: generateFunc,
		Args: cobra.NoArgs,
	}

	bundleGenerateCmd.Flags().StringVarP(&buildDir, "directory", "d", "",
		"The directory where bundle manifests for a specific version are located.")
	if err := bundleGenerateCmd.MarkFlagRequired("directory"); err != nil {
		log.Fatalf("Failed to mark `directory` flag for `generate` subcommand as required")
	}

	bundleGenerateCmd.Flags().StringVarP(&pkg, "package", "p", "",
		"The name of the package that bundle image belongs to "+
			"(Required if `directory` is not pointing to a bundle in the nested bundle format)")

	bundleGenerateCmd.Flags().StringVarP(&channels, "channels", "c", "",
		"The list of channels that bundle image belongs to"+
			"(Required if `directory` is not pointing to a bundle in the nested bundle format)")

	bundleGenerateCmd.Flags().StringVarP(&defaultChannel, "default", "e", "",
		"The default channel for the bundle image")

	bundleGenerateCmd.Flags().StringVarP(&outputDir, "output-dir", "u", "",
		"Optional output directory for operator manifests")

	return bundleGenerateCmd
}

func generateFunc(cmd *cobra.Command, _ []string) error {
	return bundle.GenerateFunc(
		buildDir,
		outputDir,
		pkg,
		channels,
		defaultChannel,
		true,
	)
}
