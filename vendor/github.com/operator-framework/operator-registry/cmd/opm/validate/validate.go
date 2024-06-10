package validate

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/pkg/lib/config"
)

func NewCmd() *cobra.Command {
	logger := logrus.New()
	validate := &cobra.Command{
		Use:   "validate <directory>",
		Short: "Validate the declarative index config",
		Long:  "Validate the declarative config JSON file(s) in a given directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			directory := args[0]
			s, err := os.Stat(directory)
			if err != nil {
				return err
			}
			if !s.IsDir() {
				return fmt.Errorf("%q is not a directory", directory)
			}

			if err := config.Validate(c.Context(), os.DirFS(directory)); err != nil {
				logger.Fatal(err)
			}
			return nil
		},
	}

	return validate
}
