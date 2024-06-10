package index

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/cmd/opm/internal/util"
	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/lib/indexer"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func newIndexPruneStrandedCmd() *cobra.Command {
	indexCmd := &cobra.Command{
		Use:   "prune-stranded",
		Short: "prune an index of stranded bundles",
		Long: `prune an index of stranded bundles - bundles that are not associated with a particular package

` + sqlite.DeprecationMessage,

		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},

		RunE: runIndexPruneStrandedCmdFunc,
		Args: cobra.NoArgs,
	}

	indexCmd.Flags().Bool("debug", false, "enable debug logging")
	indexCmd.Flags().Bool("generate", false, "if enabled, just creates the dockerfile and saves it to local disk")
	indexCmd.Flags().StringP("out-dockerfile", "d", "", "if generating the dockerfile, this flag is used to (optionally) specify a dockerfile name")
	indexCmd.Flags().StringP("from-index", "f", "", "index to prune")
	if err := indexCmd.MarkFlagRequired("from-index"); err != nil {
		logrus.Panic("Failed to set required `from-index` flag for `index prune-stranded`")
	}
	indexCmd.Flags().StringP("binary-image", "i", "", "container image for on-image `opm` command")
	indexCmd.Flags().StringP("container-tool", "c", "podman", "tool to interact with container images (save, build, etc.). One of: [docker, podman]")
	indexCmd.Flags().StringP("tag", "t", "", "custom tag for container image being built")

	if err := indexCmd.Flags().MarkHidden("debug"); err != nil {
		logrus.Panic(err.Error())
	}

	return indexCmd

}

func runIndexPruneStrandedCmdFunc(cmd *cobra.Command, _ []string) error {
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

	binaryImage, err := cmd.Flags().GetString("binary-image")
	if err != nil {
		return err
	}

	containerTool, err := cmd.Flags().GetString("container-tool")
	if err != nil {
		return err
	}

	if containerTool == "none" {
		return fmt.Errorf("none is not a valid container-tool for index prune")
	}

	tag, err := cmd.Flags().GetString("tag")
	if err != nil {
		return err
	}

	skipTLSVerify, useHTTP, err := util.GetTLSOptions(cmd)
	if err != nil {
		return err
	}

	logger := logrus.WithFields(logrus.Fields{})

	logger.Info("pruning stranded bundles from the index")

	indexPruner := indexer.NewIndexStrandedPruner(containertools.NewContainerTool(containerTool, containertools.PodmanTool), logger)

	request := indexer.PruneStrandedFromIndexRequest{
		Generate:          generate,
		FromIndex:         fromIndex,
		BinarySourceImage: binaryImage,
		OutDockerfile:     outDockerfile,
		Tag:               tag,
		SkipTLSVerify:     skipTLSVerify,
		PlainHTTP:         useHTTP,
	}

	err = indexPruner.PruneStrandedFromIndex(request)
	if err != nil {
		return err
	}

	return nil
}
