package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	genericfeatures "k8s.io/apiserver/pkg/features"
	genericserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	libcrypto "github.com/openshift/library-go/pkg/crypto"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	olminformers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	olmapiserver "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/apiserver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/openshiftconfig"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apiserver"
	genericpackageserver "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apiserver/generic"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/provider"
	apidiscovery "k8s.io/client-go/discovery"
)

const DefaultWakeupInterval = 12 * time.Hour

type Operator struct {
	queueinformer.Operator
	olmConfigQueue workqueue.TypedRateLimitingInterface[types.NamespacedName]
	options        *PackageServerOptions
}

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
	flags.DurationVar(&defaults.DefaultSyncInterval, "interval", defaults.DefaultSyncInterval, "default interval at which to re-sync CatalogSources")
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

	GlobalNamespace     string
	DefaultSyncInterval time.Duration
	CurrentSyncInterval time.Duration

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

		DefaultSyncInterval: DefaultWakeupInterval,
		CurrentSyncInterval: DefaultWakeupInterval,

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
	err := wait.PollUntilContextCancel(pollCtx, 1*time.Second, true, func(_ context.Context) (done bool, err error) {
		lastApplyErr := authenticationOptions.ApplyTo(&config.Authentication, config.SecureServing, config.OpenAPIConfig)
		if lastApplyErr != nil {
			log.WithError(lastApplyErr).Warn("Error initializing delegating authentication (will retry)")
			return false, nil
		}
		return true, nil
	})

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
	err = wait.PollUntilContextCancel(ctx, 1*time.Second, true, func(_ context.Context) (done bool, err error) {
		lastApplyErr = authorizationOptions.ApplyTo(&config.Authorization)
		if lastApplyErr != nil {
			log.WithError(lastApplyErr).Warn("Error initializing delegating authorization (will retry)")
			return false, nil
		}
		return true, nil
	})

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

	// Enables http2 DOS mitigations for unauthenticated clients.
	utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{
		string(genericfeatures.UnauthenticatedHTTP2DOSMitigation): true,
	})

	// Set up the client config before calling Config() so it can be used to
	// apply the cluster TLS security profile to the serving options.
	var clientConfig *rest.Config
	var err error
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

	// If --tls-min-version was not supplied (e.g. no PSM-injected flags yet), fall
	// back to a direct GET of the cluster APIServer CR so the packageserver still
	// honours the cluster TLS security profile on first boot or during upgrades.
	if o.SecureServing.MinTLSVersion == "" {
		// Warn and continue on failure: the API server may not be reachable
		// during initial startup (e.g. cluster bootstrap). Packageserver starts
		// with secure TLS defaults; operators can supply --tls-min-version and
		// --tls-cipher-suites flags to override the profile on a later restart.
		if err := applyClusterTLSProfile(ctx, clientConfig, o.SecureServing); err != nil {
			log.WithError(err).Warn("Failed to apply cluster TLS profile to serving options, using defaults")
		}
	}

	// Grab the config for the API server
	var config *apiserver.Config
	config, err = o.Config(ctx)
	if err != nil {
		return err
	}
	config.GenericConfig.EnableMetrics = true

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

	op := &Operator{
		Operator: queueOperator,
		olmConfigQueue: workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](
			workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
			workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
				Name: "olmConfig",
			}),
		options: o,
	}

	olmConfigInformer := olminformers.NewSharedInformerFactoryWithOptions(crClient, 0).Operators().V1().OLMConfigs()
	olmConfigQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithInformer(olmConfigInformer.Informer()),
		queueinformer.WithQueue(op.olmConfigQueue),
		queueinformer.WithIndexer(olmConfigInformer.Informer().GetIndexer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncOLMConfig).ToSyncer()),
	)
	if err != nil {
		return err
	}
	if err := op.RegisterQueueInformer(olmConfigQueueInformer); err != nil {
		return err
	}

	// Use the interval from the CLI as default
	if o.CurrentSyncInterval != o.DefaultSyncInterval {
		log.Infof("CLI argument changed default from '%v' to '%v'", o.CurrentSyncInterval, o.DefaultSyncInterval)
		o.CurrentSyncInterval = o.DefaultSyncInterval
	}
	// Use the interval from the OLMConfig
	cfg, err := crClient.OperatorsV1().OLMConfigs().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		log.Warnf("Error retrieving Interval from OLMConfig: '%v'", err)
	} else {
		if cfg.Spec.Features != nil && cfg.Spec.Features.PackageServerSyncInterval != nil {
			o.CurrentSyncInterval = cfg.Spec.Features.PackageServerSyncInterval.Duration
			log.Infof("Retrieved Interval from OLMConfig: '%v'", o.CurrentSyncInterval.String())
		} else {
			log.Infof("Defaulting Interval to '%v'", o.DefaultSyncInterval)
		}
	}

	sourceProvider, err := provider.NewRegistryProvider(ctx, crClient, queueOperator, o.CurrentSyncInterval, o.GlobalNamespace)
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

	err = server.GenericAPIServer.PrepareRun().RunWithContext(ctx)
	<-sourceProvider.Done()

	return err
}

func (op *Operator) syncOLMConfig(obj interface{}) error {
	olmConfig, ok := obj.(*operatorsv1.OLMConfig)
	if !ok {
		return fmt.Errorf("casting OLMConfig failed")
	}
	// restart the pod on change
	if olmConfig.Spec.Features == nil || olmConfig.Spec.Features.PackageServerSyncInterval == nil {
		if op.options.CurrentSyncInterval != op.options.DefaultSyncInterval {
			log.Warnf("Change to olmConfig: '%v' != default '%v'", op.options.CurrentSyncInterval, op.options.DefaultSyncInterval)
			os.Exit(0)
		}
	} else {
		if op.options.CurrentSyncInterval != olmConfig.Spec.Features.PackageServerSyncInterval.Duration {
			log.Warnf("Change to olmConfig: old '%v' != new '%v'", op.options.CurrentSyncInterval, olmConfig.Spec.Features.PackageServerSyncInterval.Duration)
			os.Exit(0)
		}
	}

	return nil
}

// applyClusterTLSProfile fetches the cluster-wide APIServer TLS security profile
// and applies it to the SecureServingOptions. It is a no-op on non-OpenShift clusters.
// This is the fallback path used when --tls-min-version is not provided via flags
// (i.e. before the PSM has had a chance to inject them).
func applyClusterTLSProfile(ctx context.Context, config *rest.Config, serving *genericoptions.SecureServingOptionsWithLoopback) error {
	const lookupTimeout = 30 * time.Second
	profileCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	profileConfig := rest.CopyConfig(config)
	if profileConfig.Timeout == 0 || profileConfig.Timeout > lookupTimeout {
		profileConfig.Timeout = lookupTimeout
	}

	kubeClient, err := kubernetes.NewForConfig(profileConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	cfgClient, err := configv1client.NewForConfig(profileConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}
	return applyClusterTLSProfileWithClients(profileCtx, kubeClient.Discovery(), cfgClient, serving)
}

// applyClusterTLSProfileWithClients is the testable core of applyClusterTLSProfile.
// It applies the cluster-wide APIServer TLS security profile to the SecureServingOptions,
// but only for fields not already set by explicit flags (--tls-min-version / --tls-cipher-suites).
// It is a no-op on non-OpenShift clusters.
func applyClusterTLSProfileWithClients(ctx context.Context, discovery apidiscovery.DiscoveryInterface, cfgClient configv1client.Interface, serving *genericoptions.SecureServingOptionsWithLoopback) error {
	available, err := openshiftconfig.IsAPIAvailable(discovery)
	if err != nil {
		return fmt.Errorf("failed to check OpenShift config API: %w", err)
	}
	if !available {
		return nil
	}

	apiServer, err := cfgClient.ConfigV1().APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get APIServer config: %w", err)
	}

	minVersion, cipherSuites := olmapiserver.GetSecurityProfileConfig(apiServer.Spec.TLSSecurityProfile)
	// Only override fields not already set by explicit flags.
	if serving.MinTLSVersion == "" {
		serving.MinTLSVersion = libcrypto.TLSVersionToNameOrDie(minVersion)
	}
	if len(serving.CipherSuites) == 0 {
		serving.CipherSuites = libcrypto.CipherSuitesToNamesOrDie(cipherSuites)
	}
	log.Infof("Applying cluster TLS security profile: minVersion=%s cipherSuites=%v", serving.MinTLSVersion, serving.CipherSuites)
	return nil
}
