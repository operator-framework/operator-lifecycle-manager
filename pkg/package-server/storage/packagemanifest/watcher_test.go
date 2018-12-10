package packagemanifest

import (
	"testing"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/provider"
)

type packageValue struct {
	name      string
	namespace string
}

func packageManifest(value packageValue) v1alpha1.PackageManifest {
	return v1alpha1.PackageManifest{
		ObjectMeta: v1.ObjectMeta{
			Name:      value.name,
			Namespace: value.namespace,
		},
	}
}

func TestAdd(t *testing.T) {
	tests := []struct {
		manifest        packageValue
		namespace       string
		name            string
		resourceVersion string
		labelSelector   labels.Selector
		description     string
	}{
		{
			manifest:        packageValue{name: "etcd", namespace: "default"},
			namespace:       v1.NamespaceAll,
			name:            "",
			resourceVersion: "",
			labelSelector:   labels.Everything(),
			description:     "All",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			stop := make(chan struct{})
			result := make(chan watch.Event, 1)

			fakeSource := provider.NewFakeProvider()
			watcher := &Watcher{
				source:        fakeSource,
				labelSelector: labels.Everything(),
				stop:          stop,
				result:        result,
			}
			manifest := packageManifest(test.manifest)
			watcher.Add(packageManifest(test.manifest))

			event := <-result

			require.Equal(t, watch.Added, event.Type)
			require.Equal(t, &manifest, event.Object)
		})
	}
}

func TestModify(t *testing.T) {
	tests := []struct {
		manifest        packageValue
		namespace       string
		name            string
		resourceVersion string
		labelSelector   labels.Selector
		description     string
	}{
		{
			manifest:        packageValue{name: "etcd", namespace: "default"},
			namespace:       v1.NamespaceAll,
			name:            "",
			resourceVersion: "",
			labelSelector:   labels.Everything(),
			description:     "All",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			stop := make(chan struct{})
			result := make(chan watch.Event, 1)

			fakeSource := provider.NewFakeProvider()
			watcher := &Watcher{
				source:        fakeSource,
				labelSelector: labels.Everything(),
				stop:          stop,
				result:        result,
			}
			manifest := packageManifest(test.manifest)
			watcher.Modify(packageManifest(test.manifest))

			event := <-result

			require.Equal(t, watch.Modified, event.Type)
			require.Equal(t, &manifest, event.Object)
		})
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		manifest        packageValue
		namespace       string
		name            string
		resourceVersion string
		labelSelector   labels.Selector
		description     string
	}{
		{
			manifest:        packageValue{name: "etcd", namespace: "default"},
			namespace:       v1.NamespaceAll,
			name:            "",
			resourceVersion: "",
			labelSelector:   labels.Everything(),
			description:     "All",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			stop := make(chan struct{})
			result := make(chan watch.Event, 1)

			fakeSource := provider.NewFakeProvider()
			watcher := &Watcher{
				source:        fakeSource,
				labelSelector: labels.Everything(),
				stop:          stop,
				result:        result,
			}
			manifest := packageManifest(test.manifest)
			watcher.Delete(packageManifest(test.manifest))

			event := <-result

			require.Equal(t, watch.Deleted, event.Type)
			require.Equal(t, &manifest, event.Object)
		})
	}
}

func TestStop(t *testing.T) {
	stop := make(chan struct{}, 1)
	result := make(chan watch.Event)
	watcher := &Watcher{stop: stop, result: result}

	watcher.Stop()

	_, open := <-result
	require.False(t, open)
}

func TestResultChan(t *testing.T) {
	result := make(chan watch.Event, 1)
	watcher := &Watcher{stop: make(chan struct{}), result: result}

	result <- watch.Event{}
	_, open := <-watcher.ResultChan()

	require.True(t, open)
}
