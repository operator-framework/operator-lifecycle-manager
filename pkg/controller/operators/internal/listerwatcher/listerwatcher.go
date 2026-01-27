package listerwatcher

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
)

func NewListerWatcher(client versioned.Interface, namespace string, override func(*metav1.ListOptions)) cache.ListerWatcher {
	// Wrap with ToListWatcherWithWatchListSemantics to signal fake client compatibility
	// See: https://github.com/kubernetes/kubernetes/issues/135895
	return cache.ToListWatcherWithWatchListSemantics(&cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			override(&options)
			return client.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(context.TODO(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			override(&options)
			return client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Watch(context.TODO(), options)
		},
	}, client)
}
