package render

import (
	"io"
	"log"
	"os"
	"text/template"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/alpha/action"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/cmd/opm/internal/util"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func NewCmd(showAlphaHelp bool) *cobra.Command {
	var (
		render           action.Render
		output           string
		imageRefTemplate string
	)
	cmd := &cobra.Command{
		Use:   "render [catalog-image | catalog-directory | bundle-image | bundle-directory | sqlite-file]...",
		Short: "Generate a stream of file-based catalog objects from catalogs and bundles",
		Long: `Generate a stream of file-based catalog objects to stdout from the provided
catalog images, file-based catalog directories, bundle images, and sqlite
database files.
`,
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			render.Refs = args

			var write func(declcfg.DeclarativeConfig, io.Writer) error
			switch output {
			case "yaml":
				write = declcfg.WriteYAML
			case "json":
				write = declcfg.WriteJSON
			default:
				log.Fatalf("invalid --output value %q, expected (json|yaml)", output)
			}

			// The bundle loading impl is somewhat verbose, even on the happy path,
			// so discard all logrus default logger logs. Any important failures will be
			// returned from render.Run and logged as fatal errors.
			logrus.SetOutput(io.Discard)

			reg, err := util.CreateCLIRegistry(cmd)
			if err != nil {
				log.Fatal(err)
			}
			defer reg.Destroy()

			render.Registry = reg

			if imageRefTemplate != "" {
				tmpl, err := template.New("image-ref-template").Parse(imageRefTemplate)
				if err != nil {
					log.Fatalf("invalid image reference template: %v", err)
				}
				render.ImageRefTemplate = tmpl
			}

			cfg, err := render.Run(cmd.Context())
			if err != nil {
				log.Fatal(err)
			}

			if err := write(*cfg, os.Stdout); err != nil {
				log.Fatal(err)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "json", "Output format of the streamed file-based catalog objects (json|yaml)")
	cmd.Flags().BoolVar(&render.Migrate, "migrate", false, "Perform migrations on the rendered FBC")

	// Alpha flags
	cmd.Flags().StringVar(&imageRefTemplate, "alpha-image-ref-template", "", "When bundle image reference information is unavailable, populate it with this template")

	if showAlphaHelp {
		cmd.Long += `
If rendering sources that do not carry bundle image reference information
(e.g. bundle directories), the --alpha-image-ref-template flag can be used to
generate image references for the rendered file-based catalog objects.
This is useful when generating a catalog with image references prior to
those images actually existing. Available template variables are:
  - {{.Package}} : the package name the bundle belongs to
  - {{.Name}}    : the name of the bundle (for registry+v1 bundles, this is the CSV name)
  - {{.Version}} : the version of the bundle
`
	}
	cmd.Long += "\n" + sqlite.DeprecationMessage
	return cmd
}
