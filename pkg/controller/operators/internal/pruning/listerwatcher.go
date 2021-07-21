package pruning

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
)

type Pruner interface {
	Prune(*v1alpha1.ClusterServiceVersion)
}

type PrunerFunc func(*v1alpha1.ClusterServiceVersion)

func (f PrunerFunc) Prune(csv *v1alpha1.ClusterServiceVersion) {
	f(csv)
}

func NewListerWatcher(client versioned.Interface, namespace string, override func(*metav1.ListOptions), p Pruner) cache.ListerWatcher {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			override(&options)
			list, err := client.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(context.TODO(), options)
			if err != nil {
				return list, err
			}
			for i := range list.Items {
				p.Prune(&list.Items[i])
			}
			return list, nil
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			override(&options)
			w, err := client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Watch(context.TODO(), options)
			if err != nil {
				return w, err
			}
			return watch.Filter(w, watch.FilterFunc(func(e watch.Event) (watch.Event, bool) {
				if csv, ok := e.Object.(*v1alpha1.ClusterServiceVersion); ok {
					p.Prune(csv)
				}
				return e, true
			})), nil
		},
	}
}
