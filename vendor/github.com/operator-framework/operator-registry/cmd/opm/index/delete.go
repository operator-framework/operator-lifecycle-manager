package index

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/cmd/opm/internal/util"
	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/lib/indexer"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func newIndexDeleteCmd() *cobra.Command {
	indexCmd := &cobra.Command{
		Use:   "rm",
		Short: "delete an entire operator from an index",
		Long: `delete an entire operator from an index

` + sqlite.DeprecationMessage,

		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},

		RunE: runIndexDeleteCmdFunc,
		Args: cobra.NoArgs,
	}

	indexCmd.Flags().Bool("debug", false, "enable debug logging")
	indexCmd.Flags().Bool("generate", false, "if enabled, just creates the dockerfile and saves it to local disk")
	indexCmd.Flags().StringP("out-dockerfile", "d", "", "if generating the dockerfile, this flag is used to (optionally) specify a dockerfile name")
	indexCmd.Flags().StringP("from-index", "f", "", "previous index to delete from")
	if err := indexCmd.MarkFlagRequired("from-index"); err != nil {
		logrus.Panic("Failed to set required `from-index` flag for `index delete`")
	}
	indexCmd.Flags().StringSliceP("operators", "o", nil, "comma separated list of operators to delete")
	if err := indexCmd.MarkFlagRequired("operators"); err != nil {
		logrus.Panic("Failed to set required `operators` flag for `index delete`")
	}
	indexCmd.Flags().StringP("binary-image", "i", "", "container image for on-image `opm` command")
	indexCmd.Flags().StringP("container-tool", "c", "", "tool to interact with container images (save, build, etc.). One of: [none, docker, podman]")
	indexCmd.Flags().StringP("build-tool", "u", "", "tool to build container images. One of: [docker, podman]. Defaults to podman. Overrides part of container-tool.")
	indexCmd.Flags().StringP("pull-tool", "p", "", "tool to pull container images. One of: [none, docker, podman]. Defaults to none. Overrides part of container-tool.")
	indexCmd.Flags().StringP("tag", "t", "", "custom tag for container image being built")
	indexCmd.Flags().Bool("permissive", false, "allow registry load errors")

	if err := indexCmd.Flags().MarkHidden("debug"); err != nil {
		logrus.Panic(err.Error())
	}

	return indexCmd

}

func runIndexDeleteCmdFunc(cmd *cobra.Command, _ []string) error {
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

	operators, err := cmd.Flags().GetStringSlice("operators")
	if err != nil {
		return err
	}

	binaryImage, err := cmd.Flags().GetString("binary-image")
	if err != nil {
		return err
	}

	pullTool, buildTool, err := getContainerTools(cmd)
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

	skipTLSVerify, useHTTP, err := util.GetTLSOptions(cmd)
	if err != nil {
		return err
	}

	logger := logrus.WithFields(logrus.Fields{"operators": operators})

	logger.Info("building the index")

	indexDeleter := indexer.NewIndexDeleter(
		containertools.NewContainerTool(buildTool, containertools.PodmanTool),
		containertools.NewContainerTool(pullTool, containertools.NoneTool),
		logger)

	request := indexer.DeleteFromIndexRequest{
		Generate:          generate,
		FromIndex:         fromIndex,
		BinarySourceImage: binaryImage,
		OutDockerfile:     outDockerfile,
		Operators:         operators,
		Tag:               tag,
		Permissive:        permissive,
		SkipTLSVerify:     skipTLSVerify,
		PlainHTTP:         useHTTP,
	}

	err = indexDeleter.DeleteFromIndex(request)
	if err != nil {
		return err
	}

	return nil
}
