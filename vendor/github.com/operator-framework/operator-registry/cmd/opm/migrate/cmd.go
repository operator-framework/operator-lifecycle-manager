package migrate

import (
	"log"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/alpha/action"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func NewCmd() *cobra.Command {
	var (
		migrate action.Migrate
		output  string
	)
	cmd := &cobra.Command{
		Use:   "migrate <indexRef> <outputDir>",
		Short: "Migrate a sqlite-based index image or database file to a file-based catalog",
		Long: `Migrate a sqlite-based index image or database file to a file-based catalog.

NOTE: the --output=json format produces streamable, concatenated JSON files.
These are suitable to opm and jq, but may not be supported by arbitrary JSON
parsers that assume that a file contains exactly one valid JSON object.

` + sqlite.DeprecationMessage,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			migrate.CatalogRef = args[0]
			migrate.OutputDir = args[1]

			switch output {
			case "yaml":
				migrate.WriteFunc = declcfg.WriteYAML
				migrate.FileExt = ".yaml"
			case "json":
				migrate.WriteFunc = declcfg.WriteJSON
				migrate.FileExt = ".json"
			default:
				log.Fatalf("invalid --output value %q, expected (json|yaml)", output)
			}

			logrus.Infof("rendering index %q as file-based catalog", migrate.CatalogRef)
			if err := migrate.Run(cmd.Context()); err != nil {
				logrus.New().Fatal(err)
			}
			logrus.Infof("wrote rendered file-based catalog to %q\n", migrate.OutputDir)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "json", "Output format (json|yaml)")
	return cmd
}
