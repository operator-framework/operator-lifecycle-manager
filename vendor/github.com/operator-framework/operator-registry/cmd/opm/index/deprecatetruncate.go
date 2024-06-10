package index

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/kubectl/pkg/util/templates"

	"github.com/operator-framework/operator-registry/cmd/opm/internal/util"
	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/lib/indexer"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

var deprecateLong = templates.LongDesc(`
	Deprecate and truncate operator bundles from an index.
	
	Deprecated bundles will no longer be installable. Bundles that are replaced by deprecated bundles will be removed entirely from the index.
	
	For example:

		Given the update graph in quay.io/my/index:v1
		1.4.0 -- replaces -> 1.3.0 -- replaces -> 1.2.0 -- replaces -> 1.1.0

		Applying the command:
		opm index deprecatetruncate --bundles "quay.io/my/bundle:1.3.0" --from-index "quay.io/my/index:v1" --tag "quay.io/my/index:v2"

		Produces the following update graph in quay.io/my/index:v2
		1.4.0 -- replaces -> 1.3.0 [deprecated]
		
	Deprecating a bundle that removes the default channel is not allowed unless the head(s) of all channels are being deprecated (the package is subsequently removed from the index). 
    This behavior can be enabled via the allow-package-removal flag. 
    Changing the default channel prior to deprecation is possible by publishing a new bundle to the index.
	`) + "\n\n" + sqlite.DeprecationMessage

func newIndexDeprecateTruncateCmd() *cobra.Command {
	indexCmd := &cobra.Command{
		Hidden: true,
		Use:    "deprecatetruncate",
		Short:  "Deprecate and truncate operator bundles from an index.",
		Long:   deprecateLong,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},
		RunE: runIndexDeprecateTruncateCmdFunc,
		Args: cobra.NoArgs,
	}

	indexCmd.Flags().Bool("debug", false, "enable debug logging")
	indexCmd.Flags().Bool("generate", false, "if enabled, just creates the dockerfile and saves it to local disk")
	indexCmd.Flags().StringP("out-dockerfile", "d", "", "if generating the dockerfile, this flag is used to (optionally) specify a dockerfile name")
	indexCmd.Flags().StringP("from-index", "f", "", "previous index to add to")
	indexCmd.Flags().StringSliceP("bundles", "b", nil, "comma separated list of bundles to add")
	if err := indexCmd.MarkFlagRequired("bundles"); err != nil {
		logrus.Panic("Failed to set required `bundles` flag for `index add`")
	}
	indexCmd.Flags().StringP("binary-image", "i", "", "container image for on-image `opm` command")
	indexCmd.Flags().StringP("container-tool", "c", "", "tool to interact with container images (save, build, etc.). One of: [docker, podman]")
	indexCmd.Flags().StringP("build-tool", "u", "", "tool to build container images. One of: [docker, podman]. Defaults to podman. Overrides part of container-tool.")
	indexCmd.Flags().StringP("pull-tool", "p", "", "tool to pull container images. One of: [none, docker, podman]. Defaults to none. Overrides part of container-tool.")
	indexCmd.Flags().StringP("tag", "t", "", "custom tag for container image being built")
	indexCmd.Flags().Bool("permissive", false, "allow registry load errors")
	if err := indexCmd.Flags().MarkHidden("debug"); err != nil {
		logrus.Panic(err.Error())
	}
	indexCmd.Flags().Bool("allow-package-removal", false, "removes the entire package if the heads of all channels in the package are deprecated")

	return indexCmd
}

func runIndexDeprecateTruncateCmdFunc(cmd *cobra.Command, _ []string) error {
	generate, err := cmd.Flags().GetBool("generate")
	if err != nil {
		return err
	}

	outDockerfile, err := cmd.Flags().GetString("out-dockerfile")
	if err != nil {
		return err
	}

	fromIndex, err := cmd.Flags().GetString("from-index")
	if err != nil {
		return err
	}

	bundles, err := cmd.Flags().GetStringSlice("bundles")
	if err != nil {
		return err
	}

	binaryImage, err := cmd.Flags().GetString("binary-image")
	if err != nil {
		return err
	}

	tag, err := cmd.Flags().GetString("tag")
	if err != nil {
		return err
	}

	permissive, err := cmd.Flags().GetBool("permissive")
	if err != nil {
		return err
	}

	pullTool, buildTool, err := getContainerTools(cmd)
	if err != nil {
		return err
	}

	skipTLSVerify, useHTTP, err := util.GetTLSOptions(cmd)
	if err != nil {
		return err
	}

	allowPackageRemoval, err := cmd.Flags().GetBool("allow-package-removal")
	if err != nil {
		return err
	}

	logger := logrus.WithFields(logrus.Fields{"bundles": bundles})

	logger.Info("deprecating bundles from the index")

	indexDeprecator := indexer.NewIndexDeprecator(
		containertools.NewContainerTool(buildTool, containertools.PodmanTool),
		containertools.NewContainerTool(pullTool, containertools.NoneTool),
		logger)

	request := indexer.DeprecateFromIndexRequest{
		Generate:            generate,
		FromIndex:           fromIndex,
		BinarySourceImage:   binaryImage,
		OutDockerfile:       outDockerfile,
		Tag:                 tag,
		Bundles:             bundles,
		Permissive:          permissive,
		SkipTLSVerify:       skipTLSVerify,
		PlainHTTP:           useHTTP,
		AllowPackageRemoval: allowPackageRemoval,
	}

	err = indexDeprecator.DeprecateFromIndex(request)
	if err != nil {
		return err
	}

	return nil
}
