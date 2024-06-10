package init

import (
	"io"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/operator-framework/operator-registry/alpha/action"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
)

func NewCmd() *cobra.Command {
	var (
		init            action.Init
		iconFile        string
		descriptionFile string
		output          string
	)
	cmd := &cobra.Command{
		Use:   "init <packageName>",
		Short: "Generate an olm.package declarative config blob",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			init.Package = args[0]

			var write func(declcfg.DeclarativeConfig, io.Writer) error
			switch output {
			case "yaml":
				write = declcfg.WriteYAML
			case "json":
				write = declcfg.WriteJSON
			default:
				log.Fatalf("invalid --output value %q, expected (json|yaml)", output)
			}

			if iconFile != "" {
				iconReader, err := os.Open(iconFile)
				if err != nil {
					log.Fatalf("open icon file: %v", err)
				}
				defer closeReader(iconReader)
				init.IconReader = iconReader
			}

			if descriptionFile != "" {
				descriptionReader, err := os.Open(descriptionFile)
				if err != nil {
					log.Fatalf("open description file: %v", err)
				}
				defer closeReader(descriptionReader)
				init.DescriptionReader = descriptionReader
			}

			pkg, err := init.Run()
			if err != nil {
				log.Fatal(err)
			}
			cfg := declcfg.DeclarativeConfig{Packages: []declcfg.Package{*pkg}}
			if err := write(cfg, os.Stdout); err != nil {
				log.Fatal(err)
			}
		},
	}
	cmd.Flags().StringVarP(&init.DefaultChannel, "default-channel", "c", "", "The channel that subscriptions will default to if unspecified")
	cmd.Flags().StringVarP(&iconFile, "icon", "i", "", "Path to package's icon")
	cmd.Flags().StringVarP(&descriptionFile, "description", "d", "", "Path to the operator's README.md (or other documentation)")
	cmd.Flags().StringVarP(&output, "output", "o", "json", "Output format (json|yaml)")
	return cmd
}

func closeReader(closer io.ReadCloser) {
	if err := closer.Close(); err != nil {
		log.Warn(err)
	}
}
