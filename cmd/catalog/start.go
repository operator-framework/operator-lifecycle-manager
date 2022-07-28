package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
	olmversion "github.com/operator-framework/operator-lifecycle-manager/pkg/version"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type options struct {
	kubeconfig           string
	catalogNamespace     string
	configMapServerImage string
	opmImage             string
	utilImage            string
	writeStatusName      string
	debug                bool
	version              bool
	profiling            bool
	tlsKeyPath           string
	tlsCertPath          string
	clientCAPath         string

	installPlanTimeout  time.Duration
	bundleUnpackTimeout time.Duration
	wakeupInterval      time.Duration
}

func newRootCmd() *cobra.Command {
	o := options{}

	cmd := &cobra.Command{
		Use:          "Start",
		Short:        "Starts the Catalog Operator",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.version {
				fmt.Print(olmversion.String())
				return nil
			}

			logger := logrus.New()
			if o.debug {
				logger.SetLevel(logrus.DebugLevel)
			}
			logger.Infof("log level %s", logger.Level)

			ctx, cancel := context.WithCancel(signals.Context())
			defer cancel()

			if err := o.run(ctx, logger); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&o.kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "absolute path to the kubeconfig file")
	cmd.Flags().StringVar(&o.catalogNamespace, "namespace", defaultCatalogNamespace, "namespace where catalog will run and install catalog resources")
	cmd.Flags().StringVar(&o.configMapServerImage, "configmapServerImage", defaultConfigMapServerImage, "the image to use for serving the operator registry api for a configmap")
	cmd.Flags().StringVar(&o.opmImage, "opmImage", defaultOPMImage, "the image to use for unpacking bundle content with opm")
	cmd.Flags().StringVar(&o.utilImage, "util-image", defaultUtilImage, "an image containing custom olm utilities")
	cmd.Flags().StringVar(&o.writeStatusName, "writeStatusName", defaultOperatorName, "ClusterOperator name in which to write status, set to \"\" to disable.")

	cmd.Flags().BoolVar(&o.debug, "debug", false, "use debug log level")
	cmd.Flags().BoolVar(&o.version, "version", false, "displays the olm version")
	cmd.Flags().BoolVar(&o.profiling, "profiling", false, "deprecated")
	cmd.Flags().MarkDeprecated("profiling", "profiling is now enabled by default")

	cmd.Flags().StringVar(&o.tlsKeyPath, "tls-key", "", "path to use for private key (requires tls-cert)")
	cmd.Flags().StringVar(&o.tlsCertPath, "tls-cert", "", "path to use for certificate key (requires tls-key)")
	cmd.Flags().StringVar(&o.clientCAPath, "client-ca", "", "path to watch for client ca bundle")

	cmd.Flags().DurationVar(&o.wakeupInterval, "interval", defaultWakeupInterval, "wakeup interval")
	cmd.Flags().DurationVar(&o.bundleUnpackTimeout, "bundle-unpack-timeout", 10*time.Minute, "The time limit for bundle unpacking, after which InstallPlan execution is considered to have failed. 0 is considered as having no timeout.")
	cmd.Flags().DurationVar(&o.installPlanTimeout, "install-plan-retry-timeout", 1*time.Minute, "time since first attempt at which plan execution errors are considered fatal")

	return cmd
}
