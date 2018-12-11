package provider

import (
	"testing"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/fakes"
)

func TestToPackageManifest(t *testing.T) {
	catsrc := &operatorsv1alpha1.CatalogSource{}
	client := &fakes.FakeRegistryClient{}
	apiPkg := &api.Package{}

	_, err := toPackageManifest(apiPkg, catsrc, client)

	require.NoError(t, err)
	// TODO(alecmerdler): Test returned object has correct fields
}

func TestRegistryProviderGet(t *testing.T) {
	// TODO(alecmerdler)
}

func TestRegistryProviderList(t *testing.T) {
	// TODO(alecmerdler)
}

func TestRegistryProviderSubscribe(t *testing.T) {
	tests := []struct {
		namespace      string
		storedPackages []packageValue
		subscribers    int
		description    string
	}{
		{
			namespace:      "default",
			storedPackages: []packageValue{},
			subscribers:    1,
			description:    "NoPackages",
		},
		{
			namespace:      "default",
			storedPackages: []packageValue{{name: "etcd", namespace: "default"}, {name: "prometheus", namespace: "local"}},
			subscribers:    1,
			description:    "SingleSubscriber",
		},
		{
			namespace:      metav1.NamespaceAll,
			storedPackages: []packageValue{{name: "etcd", namespace: "default"}, {name: "prometheus", namespace: "local"}},
			subscribers:    5,
			description:    "ManySubscribers",
		},
	}
	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			prov := &RegistryProvider{Operator: &queueinformer.Operator{}}
			stopCh := make(chan struct{})

			add, modify, delete, err := prov.Subscribe(test.namespace, stopCh)
			// TODO(alecmerdler)
		})
	}
}
