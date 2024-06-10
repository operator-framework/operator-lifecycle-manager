package root

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/operator-framework/operator-registry/cmd/opm/alpha"
	"github.com/operator-framework/operator-registry/cmd/opm/generate"
	"github.com/operator-framework/operator-registry/cmd/opm/index"
	initcmd "github.com/operator-framework/operator-registry/cmd/opm/init"
	"github.com/operator-framework/operator-registry/cmd/opm/migrate"
	"github.com/operator-framework/operator-registry/cmd/opm/registry"
	"github.com/operator-framework/operator-registry/cmd/opm/render"
	"github.com/operator-framework/operator-registry/cmd/opm/serve"
	"github.com/operator-framework/operator-registry/cmd/opm/validate"
	"github.com/operator-framework/operator-registry/cmd/opm/version"
)

func NewCmd(showAlphaHelp bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "opm",
		Short: "operator package manager",
		Long: `CLI to interact with operator-registry and build indexes of operator content.

To view help related to alpha features, set HELP_ALPHA=true in the environment.`,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if debug, _ := cmd.Flags().GetBool("debug"); debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			return nil
		},
		Args: cobra.NoArgs,
		Run:  func(_ *cobra.Command, _ []string) {}, // adding an empty function here to preserve non-zero exit status for misstated subcommands/flags for the command hierarchy
	}

	cmd.PersistentFlags().Bool("skip-tls", false, "skip TLS certificate verification for container image registries while pulling bundles or index")
	cmd.PersistentFlags().Bool("skip-tls-verify", false, "skip TLS certificate verification for container image registries while pulling bundles")
	cmd.PersistentFlags().Bool("use-http", false, "use plain HTTP for container image registries while pulling bundles")
	if err := cmd.PersistentFlags().MarkDeprecated("skip-tls", "use --use-http and --skip-tls-verify instead"); err != nil {
		logrus.Panic(err.Error())
	}

	cmd.AddCommand(registry.NewOpmRegistryCmd(showAlphaHelp), alpha.NewCmd(showAlphaHelp), initcmd.NewCmd(), migrate.NewCmd(), serve.NewCmd(), render.NewCmd(showAlphaHelp), validate.NewCmd(), generate.NewCmd())
	index.AddCommand(cmd, showAlphaHelp)
	version.AddCommand(cmd)

	cmd.Flags().Bool("debug", false, "enable debug logging")
	if err := cmd.Flags().MarkHidden("debug"); err != nil {
		logrus.Panic(err.Error())
	}

	// Mark all alpha flags as hidden and prepend their usage with an alpha warning
	configureAlphaFlags(cmd, !showAlphaHelp)

	return cmd
}

func configureAlphaFlags(cmd *cobra.Command, hideFlags bool) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if strings.HasPrefix(f.Name, "alpha-") {
			if hideFlags {
				f.Hidden = true
			}
			f.Usage = fmt.Sprintf("(ALPHA: This flag will be removed or renamed in a future release, potentially without notice) %s", f.Usage)
		}
	})
	for _, subCmd := range cmd.Commands() {
		configureAlphaFlags(subCmd, hideFlags)
	}
}
