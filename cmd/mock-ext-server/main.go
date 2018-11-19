package main

import (
	"flag"
	"log"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/apiserver/pkg/util/logs"
	genericserver "k8s.io/apiserver/pkg/server"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/mock-ext-server/apiserver"
)

// Config flags defined globally so that they appear on the test binary as well
var (
	stopCh  = genericserver.SetupSignalHandler()
	options = apiserver.NewMockExtServerOptions(os.Stdout, os.Stderr)
	cmd     = &cobra.Command{
		Short: "Launch a mock-ext-server",
		Long:  "Launch a mock-ext-server",
		RunE: func(c *cobra.Command, args []string) error {
			if err := options.Validate(); err != nil {
				return err
			}
			if err := options.RunMockExtServer(stopCh); err != nil {
				return err
			}
			return nil
		},
	}
)

func init() {
	flags := cmd.Flags()

	flags.StringVar(&options.MockGroupVersion, "mock-group-version", "", "The group version of the resources to mock")
	flags.StringVar(&options.MockKinds, "mock-kinds", "", "The kinds to mock")
	flags.StringVar(&options.OpenAPIBasePath, "open-api-base-path", "github.com/operator-framework/operator-lifecycle-manager/pkg/mock-ext-server/apis", "Base path of the directory where the mocked group version kind types should live")
	flags.StringVar(&options.Kubeconfig, "kubeconfig", options.Kubeconfig, "The path to the kubeconfig used to connect to the Kubernetes API server and the Kubelets (defaults to in-cluster config)")
	flags.BoolVar(&options.Debug, "debug", options.Debug, "use debug log level")

	options.SecureServing.AddFlags(flags)
	options.Authentication.AddFlags(flags)
	options.Authorization.AddFlags(flags)
	options.Features.AddFlags(flags)

	flags.AddGoFlagSet(flag.CommandLine)
	flags.Parse(flag.Args())
}

func main() {
	logs.InitLogs()
	defer logs.FlushLogs()

	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
