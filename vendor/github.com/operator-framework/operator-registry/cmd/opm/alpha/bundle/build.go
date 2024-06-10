package bundle

import (
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	buildDir       string
	tag            string
	containerTool  string
	pkg            string
	channels       string
	defaultChannel string
	outputDir      string
	overwrite      bool
)

// newBundleBuildCmd returns a command that will build operator bundle image.
func newBundleBuildCmd() *cobra.Command {
	bundleBuildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build operator bundle image",
		Long: `The "opm alpha bundle build" command will generate operator
bundle metadata if needed and build bundle image with operator manifest
and metadata for a specific version.

For example: The command will generate annotations.yaml metadata plus
Dockerfile for bundle image and then build a container image from
provided operator bundle manifests generated metadata
e.g. "quay.io/example/operator:v0.0.1".

After the build process is completed, a container image would be built
locally in docker and available to push to a container registry.

$ opm alpha bundle build --directory /test/0.1.0/ --tag quay.io/example/operator:v0.1.0 \
	--package test-operator --channels stable,beta --default stable --overwrite

Note:
* Bundle image is not runnable.
* All manifests yaml must be in the same directory. `,
		RunE: buildFunc,
		Args: cobra.NoArgs,
	}

	bundleBuildCmd.Flags().StringVarP(&buildDir, "directory", "d", "",
		"The directory where bundle manifests and metadata for a specific version are located")
	if err := bundleBuildCmd.MarkFlagRequired("directory"); err != nil {
		log.Fatalf("Failed to mark `directory` flag for `build` subcommand as required")
	}

	bundleBuildCmd.Flags().StringVarP(&tag, "tag", "t", "",
		"The image tag applied to the bundle image")
	if err := bundleBuildCmd.MarkFlagRequired("tag"); err != nil {
		log.Fatalf("Failed to mark `tag` flag for `build` subcommand as required")
	}

	bundleBuildCmd.Flags().StringVarP(&pkg, "package", "p", "",
		"The name of the package that bundle image belongs to "+
			"(Required if `directory` is not pointing to a bundle in the nested bundle format)")

	bundleBuildCmd.Flags().StringVarP(&channels, "channels", "c", "",
		"The list of channels that bundle image belongs to"+
			"(Required if `directory` is not pointing to a bundle in the nested bundle format)")

	bundleBuildCmd.Flags().StringVarP(&containerTool, "image-builder", "b", "docker",
		"Tool used to manage container images. One of: [docker, podman, buildah]")

	bundleBuildCmd.Flags().StringVarP(&defaultChannel, "default", "e", "",
		"The default channel for the bundle image")

	bundleBuildCmd.Flags().BoolVarP(&overwrite, "overwrite", "o", false,
		"To overwrite annotations.yaml locally if existed. By default, overwrite is set to `false`.")

	bundleBuildCmd.Flags().StringVarP(&outputDir, "output-dir", "u", "",
		"Optional output directory for operator manifests")

	return bundleBuildCmd
}

func buildFunc(cmd *cobra.Command, _ []string) error {
	return bundle.BuildFunc(
		buildDir,
		outputDir,
		tag,
		containerTool,
		pkg,
		channels,
		defaultChannel,
		overwrite,
	)
}
