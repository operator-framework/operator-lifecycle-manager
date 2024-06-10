package template

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "render-template",
		Short: "Render a catalog template type",
		Args:  cobra.NoArgs,
	}

	runCmd.AddCommand(newBasicTemplateCmd())
	runCmd.AddCommand(newSemverTemplateCmd())
	runCmd.AddCommand(newCompositeTemplateCmd())

	return runCmd
}

func openFileOrStdin(cmd *cobra.Command, args []string) (io.ReadCloser, string, error) {
	if len(args) == 0 || args[0] == "-" {
		return io.NopCloser(cmd.InOrStdin()), "stdin", nil
	}
	reader, err := os.Open(args[0])
	return reader, args[0], err
}
