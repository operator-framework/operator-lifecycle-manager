package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/wait"
	genericserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apiserver"
	genericpackageserver "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apiserver/generic"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/provider"
)

// NewCommandStartPackageServer provides a CLI handler for 'start master' command
// with a default PackageServerOptions.
func NewCommandStartPackageServer(ctx context.Context, defaults *PackageServerOptions) *cobra.Command {
	cmd := &cobra.Command{
		Short: "Launch a package API server",
		Long:  "Launch a package API server",
		RunE: func(c *cobra.Command, args []string) error {
			if err := defaults.Run(ctx); err != nil {
				return err
			}
			return nil
		},
	}

	flags := cmd.Flags()
	flags.DurationVar(&defaults.WakeupInterval, "interval", defaults.WakeupInterval, "interval at which to re-sync CatalogSources")
	flags.StringVar(&defaults.GlobalNamespace, "global-namespace", defaults.GlobalNamespace, "Name of the namespace where the global CatalogSources are located")
	flags.StringVar(&defaults.Kubeconfig, "kubeconfig", defaults.Kubeconfig, "path to the kubeconfig used to connect to the Kubernetes API server and the Kubelets (defaults to in-cluster config)")
	flags.BoolVar(&defaults.Debug, "debug", defaults.Debug, "use debug log level")

	defaults.SecureServing.AddFlags(flags)
	defaults.Authentication.AddFlags(flags)
	defaults.Authorization.AddFlags(flags)
	defaults.Features.AddFlags(flags)

	return cmd
}

type PackageServerOptions struct {
	SecureServing  *genericoptions.SecureServingOptionsWithLoopback
	Authentication *genericoptions.DelegatingAuthenticationOptions
	Authorization  *genericoptions.DelegatingAuthorizationOptions
	Features       *genericoptions.FeatureOptions

	GlobalNamespace string
	WakeupInterval  time.Duration

	Kubeconfig   string
	RegistryAddr string

	// Only to be used to for testing
	DisableAuthForTesting bool

	// Enable debug log level
	Debug bool

	SharedInformerFactory informers.SharedInformerFactory
	StdOut                io.Writer
	StdErr                io.Writer
}

func NewPackageServerOptions(out, errOut io.Writer) *PackageServerOptions {
	o := &PackageServerOptions{
		SecureServing:  genericoptions.NewSecureServingOptions().WithLoopback(),
		Authentication: genericoptions.NewDelegatingAuthenticationOptions(),
		Authorization:  genericoptions.NewDelegatingAuthorizationOptions(),
		Features:       genericoptions.NewFeatureOptions(),

		WakeupInterval: 5 * time.Minute,

		DisableAuthForTesting: false,
		Debug:                 false,

		StdOut: out,
		StdErr: errOut,
	}

	return o
}

// Config returns config for the PackageServerOptions.
func (o *PackageServerOptions) Config(ctx context.Context) (*apiserver.Config, error) {
	if err := o.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	config := genericserver.NewConfig(genericpackageserver.Codecs)
	if err := o.SecureServing.ApplyTo(&config.SecureServing, &config.LoopbackClientConfig); err != nil {
		return nil, err
	}

	serverConfig := &apiserver.Config{
		GenericConfig:  config,
		ProviderConfig: genericpackageserver.ProviderConfig{},
	}

	if o.DisableAuthForTesting {
		return serverConfig, nil
	}

	// See https://github.com/openshift/library-go/blob/7a65fdb398e28782ee1650959a5e0419121e97ae/pkg/config/serving/server.go#L61-L63 for details on
	// the following auth/z config
	pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	authenticationOptions := genericoptions.NewDelegatingAuthenticationOptions()
	authenticationOptions.RemoteKubeConfigFile = o.Kubeconfig

	// The platform generally uses 30s for /metrics scraping, avoid API request for every other /metrics request to the component
	authenticationOptions.CacheTTL = 35 * time.Second

	// In some cases the API server can return connection refused when getting the "extension-apiserver-authentication" config map
	var lastApplyErr error
	err := wait.PollImmediateUntil(1*time.Second, func() (done bool, err error) {
		lastApplyErr := authenticationOptions.ApplyTo(&config.Authentication, config.SecureServing, config.OpenAPIConfig)
		if lastApplyErr != nil {
			log.WithError(lastApplyErr).Warn("Error initializing delegating authentication (will retry)")
			return false, nil
		}
		return true, nil
	}, pollCtx.Done())

	if err != nil {
		return nil, lastApplyErr
	}

	if err := o.Authentication.ApplyTo(&config.Authentication, config.SecureServing, nil); err != nil {
		return nil, err
	}

	authorizationOptions := genericoptions.NewDelegatingAuthorizationOptions().
		WithAlwaysAllowPaths("/healthz", "/readyz", "/livez"). // This allows the kubelet to always get health and readiness without causing an access check
		WithAlwaysAllowGroups("system:masters")                // in a kube cluster, system:masters can take any action, so there is no need to ask for an authz check
	authenticationOptions.RemoteKubeConfigFile = o.Kubeconfig

	// The platform generally uses 30s for /metrics scraping, avoid API request for every other /metrics request to the component
	authorizationOptions.AllowCacheTTL = 35 * time.Second

	// In some cases the API server can return connection refused when getting the "extension-apiserver-authentication" config map
	err = wait.PollImmediateUntil(1*time.Second, func() (done bool, err error) {
		lastApplyErr = authorizationOptions.ApplyTo(&config.Authorization)
		if lastApplyErr != nil {
			log.WithError(lastApplyErr).Warn("Error initializing delegating authorization (will retry)")
			return false, nil
		}
		return true, nil
	}, pollCtx.Done())

	if err != nil {
		return nil, lastApplyErr
	}

	if err := o.Authorization.ApplyTo(&config.Authorization); err != nil {
		return nil, err
	}

	return serverConfig, nil
}

// Run starts a new packageserver for the PackageServerOptions.
func (o *PackageServerOptions) Run(ctx context.Context) error {
	if o.Debug {
		log.SetLevel(log.DebugLevel)
	}

	// Grab the config for the API server
	config, err := o.Config(ctx)
	if err != nil {
		return err
	}
	config.GenericConfig.EnableMetrics = true

	// Set up the client config
	var clientConfig *rest.Config
	if len(o.Kubeconfig) > 0 {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: o.Kubeconfig}
		loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})

		clientConfig, err = loader.ClientConfig()
	} else {
		clientConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return fmt.Errorf("unable to construct lister client config: %v", err)
	}

	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return fmt.Errorf("unable to construct lister client: %v", err)
	}

	crClient, err := client.NewClient(o.Kubeconfig)
	if err != nil {
		return err
	}

	queueOperator, err := queueinformer.NewOperator(crClient.Discovery())
	if err != nil {
		return err
	}

	sourceProvider, err := provider.NewRegistryProvider(ctx, crClient, queueOperator, o.WakeupInterval, o.GlobalNamespace)
	if err != nil {
		return err
	}
	config.ProviderConfig.Provider = sourceProvider

	// We should never need to resync, since we're not worried about missing events,
	// and resync is actually for regular interval-based reconciliation these days,
	// so set the default resync interval to 0
	informerFactory := informers.NewSharedInformerFactory(kubeClient, 0)

	server, err := config.Complete(informerFactory).New()
	if err != nil {
		return err
	}

	sourceProvider.Run(ctx)
	<-sourceProvider.Ready()

	err = server.GenericAPIServer.PrepareRun().Run(ctx.Done())
	<-sourceProvider.Done()

	return err
}
