package catalog

import (
	"k8s.io/client-go/dynamic"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clients"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

type stubClientFactory struct {
	operatorClient   operatorclient.ClientInterface
	kubernetesClient versioned.Interface
	dynamicClient    dynamic.Interface
}

var _ clients.Factory = &stubClientFactory{}

func (f *stubClientFactory) WithConfigTransformer(clients.ConfigTransformer) clients.Factory {
	return f
}

func (f *stubClientFactory) NewOperatorClient() (operatorclient.ClientInterface, error) {
	return f.operatorClient, nil
}

func (f *stubClientFactory) NewKubernetesClient() (versioned.Interface, error) {
	return f.kubernetesClient, nil
}

func (f *stubClientFactory) NewDynamicClient() (dynamic.Interface, error) {
	return f.dynamicClient, nil
}
