package apiserver

import (
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	operatorsinformers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain/install"
	iocontroller "github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/controller/installedoperator"
	ioregistry "github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/registry/porcelain/installedoperator"
)

var (
	// Scheme defines methods for serializing and deserializing API objects.
	Scheme = runtime.NewScheme()
	// Codecs provides methods for retrieving codecs and serializers for specific
	// versions and content types.
	Codecs = serializer.NewCodecFactory(Scheme)
)

func init() {
	install.Install(Scheme)

	// we need to add the options to empty v1
	// TODO fix the server code to avoid this
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})

	// TODO: keep the generic API server from wanting this
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}

// ExtraConfig holds custom apiserver config.
type ExtraConfig struct {
	// Client is used to create events in the apiserver's Controller.
	Client kubernetes.Interface

	// OperatorsSharedInformerFactory is used to build operators.coreos.com informers for the apiserver's Controller.
	OperatorsSharedInformerFactory operatorsinformers.SharedInformerFactory

	// SharedInformerFactory is used to build k8s informers for the apiserver's Controller.
	SharedInformerFactory informers.SharedInformerFactory
}

// Config defines the config for the apiserver.
type Config struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   ExtraConfig
}

// RuntimeServer contains state for a Kubernetes cluster master/api server.
type RuntimeServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
	Controller       *iocontroller.Controller
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig
	ExtraConfig   *ExtraConfig
}

// CompletedConfig embeds a private pointer that cannot be instantiated outside of this package.
type CompletedConfig struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedConfig
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (cfg *Config) Complete() CompletedConfig {
	c := completedConfig{
		cfg.GenericConfig.Complete(),
		&cfg.ExtraConfig,
	}

	// TODO: Pull version from fields set with ld flags
	c.GenericConfig.Version = &version.Info{
		Major: "1",
		Minor: "0",
	}

	return CompletedConfig{&c}
}

// New returns a new instance of RuntimeServer from the given config.
func (c completedConfig) New() (*RuntimeServer, error) {
	genericServer, err := c.GenericConfig.New("porcelain-server", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	s := &RuntimeServer{
		GenericAPIServer: genericServer,
	}

	informersFactory := c.ExtraConfig.SharedInformerFactory.Core()
	nsInformer := informersFactory.V1().Namespaces()
	operatorsInformerFactory := c.ExtraConfig.OperatorsSharedInformerFactory.Operators()
	csvInformer := operatorsInformerFactory.V1alpha1().ClusterServiceVersions()
	registry, err := ioregistry.NewREST(Scheme, c.GenericConfig.RESTOptionsGetter, nsInformer, csvInformer)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to intialize installed registry")
	}
	v1alpha1storage := map[string]rest.Storage{
		"installedoperators": registry,
	}

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(porcelain.GroupName, Scheme, metav1.ParameterCodec, Codecs)
	apiGroupInfo.VersionedResourcesStorageMap["v1alpha1"] = v1alpha1storage
	if err := s.GenericAPIServer.InstallAPIGroup(&apiGroupInfo); err != nil {
		return nil, err
	}

	// Create the apiserver's Controller
	s.Controller = iocontroller.NewController(
		c.ExtraConfig.Client,
		registry,
		csvInformer,
		operatorsInformerFactory.V1alpha1().Subscriptions(),
		operatorsInformerFactory.V1().OperatorGroups(),
	)

	return s, nil
}
