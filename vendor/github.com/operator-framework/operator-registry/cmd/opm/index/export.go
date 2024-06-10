package index

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/kubectl/pkg/util/templates"

	"github.com/operator-framework/operator-registry/cmd/opm/internal/util"
	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/lib/indexer"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

var exportLong = templates.LongDesc(`
	Export an operator from an index image into the appregistry format. 

	This command will take an index image (specified by the --index option), parse it for the given operator(s) (set by 
	the --package option) and export the operator metadata into an appregistry compliant format (a package.yaml file). 

	Note: the appregistry format is being deprecated in favor of the new index image and image bundle format. 
	`) + "\n\n" + sqlite.DeprecationMessage

func newIndexExportCmd() *cobra.Command {
	indexCmd := &cobra.Command{
		Use:   "export",
		Short: "Export an operator from an index into the appregistry format",
		Long:  exportLong,

		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},

		RunE: runIndexExportCmdFunc,
		Args: cobra.NoArgs,
	}
	indexCmd.Flags().Bool("debug", false, "enable debug logging")
	indexCmd.Flags().StringP("index", "i", "", "index to get package from")
	if err := indexCmd.MarkFlagRequired("index"); err != nil {
		logrus.Panic("Failed to set required `index` flag for `index export`")
	}
	indexCmd.Flags().StringSliceP("package", "p", nil, "comma separated list of packages to export")
	indexCmd.Flags().StringP("download-folder", "f", "downloaded", "directory where downloaded operator bundle(s) will be stored")
	indexCmd.Flags().StringP("container-tool", "c", "none", "tool to interact with container images (save, build, etc.). One of: [none, docker, podman]")
	if err := indexCmd.Flags().MarkHidden("debug"); err != nil {
		logrus.Panic(err.Error())
	}

	// Create hidden option so we can provide deprecated shorthand
	indexCmd.Flags().StringSliceP("xpackage", "o", nil, "deprecated, please use --package option instead")
	if err := indexCmd.Flags().MarkHidden("xpackage"); err != nil {
		logrus.Panic(err.Error())
	}

	return indexCmd

}

func runIndexExportCmdFunc(cmd *cobra.Command, _ []string) error {
	index, err := cmd.Flags().GetString("index")
	if err != nil {
		return err
	}

	pkgFlag := cmd.Flag("package")
	if pkgFlag == nil {
		return fmt.Errorf("unable to get the package flag")
	}

	xPkgFlag := cmd.Flag("xpackage")
	if xPkgFlag == nil {
		return fmt.Errorf("unable to get the package flag for deprecated shorthand '-o'")
	}

	if xPkgFlag.Changed && pkgFlag.Changed {
		return fmt.Errorf("cannot simultaneously set '-p' and '-o' flags, remove '-o'")
	}

	packages, err := cmd.Flags().GetStringSlice("package")
	if err != nil {
		return err
	}
	if xPkgFlag.Changed {
		// Use the deprecated shorthand
		if packages, err = cmd.Flags().GetStringSlice("xpackage"); err != nil {
			return err
		}
	}

	downloadPath, err := cmd.Flags().GetString("download-folder")
	if err != nil {
		return err
	}

	containerTool, err := cmd.Flags().GetString("container-tool")
	if err != nil {
		return err
	}

	skipTLSVerify, useHTTP, err := util.GetTLSOptions(cmd)
	if err != nil {
		return err
	}

	logger := logrus.WithFields(logrus.Fields{"index": index, "package": packages})

	logger.Info("export from the index")

	indexExporter := indexer.NewIndexExporter(containertools.NewContainerTool(containerTool, containertools.NoneTool), logger)

	request := indexer.ExportFromIndexRequest{
		Index:         index,
		Packages:      packages,
		DownloadPath:  downloadPath,
		ContainerTool: containertools.NewContainerTool(containerTool, containertools.NoneTool),
		SkipTLSVerify: skipTLSVerify,
		PlainHTTP:     useHTTP,
	}

	err = indexExporter.ExportFromIndex(request)
	if err != nil {
		return err
	}

	return nil
}
