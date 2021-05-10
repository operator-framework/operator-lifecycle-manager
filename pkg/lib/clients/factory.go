package clients

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

type ConfigTransformer interface {
	// Applies a transformation to the provided Config and returns
	// the resulting Config. The provided Config is safe to return
	// directly (i.e., without making a copy) and will never be
	// nil. Implementations must never return nil.
	TransformConfig(config *rest.Config) *rest.Config
}

type ConfigTransformerFunc func(config *rest.Config) *rest.Config

func (t ConfigTransformerFunc) TransformConfig(config *rest.Config) *rest.Config {
	return t(config)
}

type Factory interface {
	WithConfigTransformer(ConfigTransformer) Factory
	NewOperatorClient() (operatorclient.ClientInterface, error)
	NewKubernetesClient() (versioned.Interface, error)
	NewDynamicClient() (dynamic.Interface, error)
}

type factory struct {
	config *rest.Config
}

func NewFactory(config *rest.Config) Factory {
	return &factory{config: config}
}

// WithConfigTransformer returns a new factory that produces clients
// using a copy of the receiver's REST config that has been
// transformed by the given ConfigTransformer.
func (f *factory) WithConfigTransformer(t ConfigTransformer) Factory {
	return &factory{config: t.TransformConfig(rest.CopyConfig(f.config))}
}

func (f *factory) NewOperatorClient() (operatorclient.ClientInterface, error) {
	return operatorclient.NewClientFromRestConfig(f.config)
}

func (f *factory) NewKubernetesClient() (versioned.Interface, error) {
	return versioned.NewForConfig(f.config)
}

func (f *factory) NewDynamicClient() (dynamic.Interface, error) {
	return dynamic.NewForConfig(f.config)
}
