package bundle

import (
	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "bundle",
		Short: "Operator bundle commands",
		Long:  `Generate operator bundle metadata and build bundle image.`,
		Args:  cobra.NoArgs,
	}

	runCmd.AddCommand(newBundleGenerateCmd())
	runCmd.AddCommand(newBundleBuildCmd())
	runCmd.AddCommand(newBundleValidateCmd())
	runCmd.AddCommand(extractCmd)
	runCmd.AddCommand(newBundleUnpackCmd())

	return runCmd
}
