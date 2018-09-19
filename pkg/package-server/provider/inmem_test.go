package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	packagev1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
)

type packageValue struct {
	name      string
	namespace string
}

func packageManifest(value packageValue) packagev1alpha1.PackageManifest {
	return packagev1alpha1.PackageManifest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      value.name,
			Namespace: value.namespace,
		},
	}
}

func TestList(t *testing.T) {
	tests := []struct {
		namespace        string
		storedPackages   []packageValue
		expectedPackages []packageValue
		description      string
	}{
		{
			namespace:        "default",
			storedPackages:   []packageValue{},
			expectedPackages: []packageValue{},
			description:      "NoPackages",
		},
		{
			namespace:        "default",
			storedPackages:   []packageValue{{name: "etcd", namespace: "default"}, {name: "prometheus", namespace: "local"}},
			expectedPackages: []packageValue{{name: "etcd", namespace: "default"}},
			description:      "FilterNamespace",
		},
		{
			namespace:        metav1.NamespaceAll,
			storedPackages:   []packageValue{{name: "etcd", namespace: "default"}, {name: "prometheus", namespace: "local"}},
			expectedPackages: []packageValue{{name: "etcd", namespace: "default"}, {name: "prometheus", namespace: "local"}},
			description:      "AllNamespaces",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			storedPackages := make(map[packageKey]packagev1alpha1.PackageManifest)
			for _, value := range test.storedPackages {
				storedPackages[packageKey{catalogSourceName: "test", catalogSourceNamespace: "default", packageName: value.name}] = packageManifest(value)
			}

			prov := &InMemoryProvider{
				Operator:  &queueinformer.Operator{},
				manifests: storedPackages,
			}

			manifests, err := prov.List(test.namespace)

			require.NoError(t, err)
			require.Equal(t, len(test.expectedPackages), len(manifests.Items))
			for _, expected := range test.expectedPackages {
				require.Contains(t, manifests.Items, packageManifest(expected))
			}
		})
	}
}

func TestGet(t *testing.T) {
	tests := []struct {
		namespace       string
		packageName     string
		storedPackages  []packageValue
		expectedPackage packageValue
		description     string
	}{
		{
			namespace:       "default",
			packageName:     "etcd",
			storedPackages:  []packageValue{},
			expectedPackage: packageValue{},
			description:     "NoPackages",
		},
		{
			namespace:       "default",
			packageName:     "etcd",
			storedPackages:  []packageValue{{name: "etcd", namespace: "default"}, {name: "prometheus", namespace: "local"}},
			expectedPackage: packageValue{name: "etcd", namespace: "default"},
			description:     "SingleMatch",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			storedPackages := make(map[packageKey]packagev1alpha1.PackageManifest)
			for _, value := range test.storedPackages {
				storedPackages[packageKey{catalogSourceName: "test", catalogSourceNamespace: "default", packageName: value.name}] = packageManifest(value)
			}

			prov := &InMemoryProvider{
				Operator:  &queueinformer.Operator{},
				manifests: storedPackages,
			}

			manifest, err := prov.Get(test.namespace, test.packageName)

			require.NoError(t, err)
			require.EqualValues(t, packageManifest(test.expectedPackage), *manifest)
		})
	}
}

func TestSubscribe(t *testing.T) {
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

	type subscriber struct {
		add    <-chan packagev1alpha1.PackageManifest
		modify <-chan packagev1alpha1.PackageManifest
		delete <-chan packagev1alpha1.PackageManifest
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			prov := &InMemoryProvider{Operator: &queueinformer.Operator{}}
			stopCh := make(chan struct{})

			subscribers := make([]subscriber, test.subscribers)
			for i := range subscribers {
				add, modify, delete, err := prov.Subscribe(stopCh)
				require.NoError(t, err)
				subscribers[i] = subscriber{add, modify, delete}
			}

			for _, add := range prov.add {
				go func(addCh chan packagev1alpha1.PackageManifest) {
					for _, value := range test.storedPackages {
						addCh <- packageManifest(value)
					}
					close(addCh)
					return
				}(add)
			}

			for _, sub := range subscribers {
				i := 0
				for manifest := range sub.add {
					require.EqualValues(t, manifest, packageManifest(test.storedPackages[i]))
					i = i + 1
				}
			}
		})
	}
}
