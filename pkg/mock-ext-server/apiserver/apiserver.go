package apiserver

import (
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/go-openapi/inflect"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	registryrest "k8s.io/apiserver/pkg/registry/rest"
	genericserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/mock-ext-server/apis/anything/v1alpha1"
	generatedopenapi "github.com/operator-framework/operator-lifecycle-manager/pkg/mock-ext-server/openapi"
	anythingstorage "github.com/operator-framework/operator-lifecycle-manager/pkg/mock-ext-server/storage/anything"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/mock-ext-server/version"
)

var (
	v1GroupVersion = schema.GroupVersion{Group: "", Version: "v1"}

	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
	// parameterCodec = runtime.NewParameterCodec(scheme)
)

func init() {
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Version: "v1"})

	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}

// MockExtServerOptions represents configuration options for a mock extension api-server
type MockExtServerOptions struct {
	SecureServing  *genericoptions.SecureServingOptionsWithLoopback
	Authentication *genericoptions.DelegatingAuthenticationOptions
	Authorization  *genericoptions.DelegatingAuthorizationOptions
	Features       *genericoptions.FeatureOptions

	MockGroupVersion string
	MockKinds        string
	OpenAPIBasePath  string

	Kubeconfig string

	// Only to be used to for testing
	DisableAuthForTesting bool

	// Enable debug log level
	Debug bool

	SharedInformerFactory informers.SharedInformerFactory
	StdOut                io.Writer
	StdErr                io.Writer
}

// Config creates a MockExtServerConfig from the MockExtServerOptions
func (options *MockExtServerOptions) Config() (*MockExtServerConfig, error) {
	if err := options.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	config := genericserver.NewConfig(codecs)
	if err := options.SecureServing.ApplyTo(config); err != nil {
		return nil, err
	}

	if !options.DisableAuthForTesting {
		if err := options.Authentication.ApplyTo(&config.Authentication, config.SecureServing, nil); err != nil {
			return nil, err
		}
		if err := options.Authorization.ApplyTo(&config.Authorization); err != nil {
			return nil, err
		}
	}

	// Parse the GroupVersion
	groupVersion, err := schema.ParseGroupVersion(options.MockGroupVersion)
	if err != nil {
		return nil, err
	}

	// Split kinds
	kinds := strings.Split(options.MockKinds, ",")
	log.Warnf("len(kinds): %d", len(kinds))

	return &MockExtServerConfig{
		Config:           config,
		mockGroupVersion: groupVersion,
		mockKinds:        kinds,
		openAPIBasePath:  options.OpenAPIBasePath,
	}, nil
}

// Validate validates the MockExtServerOptions
func (options *MockExtServerOptions) Validate() error {
	errors := []error{}
	errors = append(errors, options.SecureServing.Validate()...)
	errors = append(errors, options.Authentication.Validate()...)
	errors = append(errors, options.Authorization.Validate()...)
	errors = append(errors, options.Features.Validate()...)

	// Attempt to Parse the group version
	_, err := schema.ParseGroupVersion(options.MockGroupVersion)
	errors = append(errors, err)

	if len(strings.Split(options.MockKinds, ",")) == 0 {
		err := fmt.Errorf("mock kinds cannot be empty")
		errors = append(errors, err)
	}

	// TODO: Add validation for OpenAPIBasePath

	return utilerrors.NewAggregate(errors)
}

// NewMockExtServerOptions returns a pointer to a new MockExtServerOptions instance with partially defaulted properties
func NewMockExtServerOptions(out, errOut io.Writer) *MockExtServerOptions {
	options := &MockExtServerOptions{
		SecureServing:  genericoptions.WithLoopback(genericoptions.NewSecureServingOptions()),
		Authentication: genericoptions.NewDelegatingAuthenticationOptions(),
		Authorization:  genericoptions.NewDelegatingAuthorizationOptions(),
		Features:       genericoptions.NewFeatureOptions(),

		DisableAuthForTesting: false,
		Debug:                 false,

		StdOut: out,
		StdErr: errOut,
	}

	return options
}

func (options *MockExtServerOptions) Run(stopCh <-chan struct{}) error {
	if options.Debug {
		log.SetLevel(log.DebugLevel)
	}

	// Grab the config for the API server
	config, err := options.Config()
	if err != nil {
		return err
	}
	config.EnableMetrics = true

	// Set up the client config
	var clientConfig *rest.Config
	if len(options.Kubeconfig) > 0 {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: options.Kubeconfig}
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

	informerFactory := informers.NewSharedInformerFactory(kubeClient, 0)
	server, err := config.Complete(informerFactory).New("mock-ext-server", genericserver.NewEmptyDelegate())
	if err != nil {
		return err
	}

	return server.PrepareRun().Run(stopCh)
}

// MockExtServerConfig is a wrapper around Config
type MockExtServerConfig struct {
	*genericserver.Config
	mockGroupVersion schema.GroupVersion
	mockKinds        []string
	openAPIBasePath  string
}

func (config *MockExtServerConfig) Complete(informers informers.SharedInformerFactory) genericserver.CompletedConfig {
	config.Version = version.VersionInfo()

	// Add known types to scheme
	for _, kind := range config.mockKinds {
		gvk := config.mockGroupVersion.WithKind(kind)
		log.Infof("adding known type to scheme: %+v", gvk)
		scheme.AddKnownTypeWithName(gvk, &v1alpha1.Anything{})
	}
	scheme.AddKnownTypeWithName(config.mockGroupVersion.WithKind("Anything"), &v1alpha1.Anything{})
	metav1.AddToGroupVersion(scheme, config.mockGroupVersion)

	// Enable OpenAPI
	config.OpenAPIConfig = genericserver.DefaultOpenAPIConfig(generatedopenapi.GetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(scheme))
	config.OpenAPIConfig.Info.Title = "MockExtServer"
	config.OpenAPIConfig.Info.Version = strings.Split(config.Version.String(), "-")[0]
	config.SwaggerConfig = genericserver.DefaultSwaggerConfig()

	return config.Config.Complete(informers)
}

func (options *MockExtServerOptions) RunMockExtServer(stopCh <-chan struct{}) error {
	if options.Debug {
		log.SetLevel(log.DebugLevel)
	}

	// grab the config for the API server
	config, err := options.Config()
	if err != nil {
		return err
	}
	config.EnableMetrics = true

	// set up the client config
	var clientConfig *rest.Config
	if len(options.Kubeconfig) > 0 {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: options.Kubeconfig}
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

	informerFactory := informers.NewSharedInformerFactory(kubeClient, 0)
	server, err := config.Complete(informerFactory).New("mock-ext-server", genericserver.NewEmptyDelegate())
	if err != nil {
		return err
	}

	// Install APIGroups
	apiGroupInfo := genericserver.NewDefaultAPIGroupInfo(config.mockGroupVersion.Group, scheme, metav1.ParameterCodec, codecs)
	anythingResources := make(map[string]registryrest.Storage)
	for _, kind := range config.mockKinds {
		plural := strings.ToLower(inflect.Pluralize(kind))
		anythingResources[plural] = anythingstorage.NewStorage(kind)
	}
	apiGroupInfo.VersionedResourcesStorageMap[config.mockGroupVersion.Version] = anythingResources

	err = server.InstallAPIGroup(&apiGroupInfo)
	if err != nil {
		log.Warnf("Error installing APIGroup: %s", err.Error())
	} else {
		log.Infof("APIGroup install successful")
	}

	return server.PrepareRun().Run(stopCh)
}
