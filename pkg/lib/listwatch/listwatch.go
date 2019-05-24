package listwatch

import (
	mlw "github.com/coreos/prometheus-operator/pkg/listwatch"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
)

// TODO: Figure out how to inject multi-namespace ListerWatchers into typed informer factories.

func CatalogSourceListerWatcher(client versioned.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.OperatorsV1alpha1().CatalogSources(namespace).List(options)
			},
			WatchFunc: client.OperatorsV1alpha1().CatalogSources(namespace).Watch,
		}
	})
}

func ClusterServiceVersionListerWatcher(client versioned.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(options)
			},
			WatchFunc: client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Watch,
		}
	})
}

func InstallPlanListerWatcher(client versioned.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.OperatorsV1alpha1().InstallPlans(namespace).List(options)
			},
			WatchFunc: client.OperatorsV1alpha1().InstallPlans(namespace).Watch,
		}
	})
}

func SubscriptionListerWatcher(client versioned.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.OperatorsV1alpha1().Subscriptions(namespace).List(options)
			},
			WatchFunc: client.OperatorsV1alpha1().Subscriptions(namespace).Watch,
		}
	})
}

func OperatorGroupListerWatcher(client versioned.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.OperatorsV1().OperatorGroups(namespace).List(options)
			},
			WatchFunc: client.OperatorsV1().OperatorGroups(namespace).Watch,
		}
	})
}

func RoleListerWatcher(client kubernetes.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.RbacV1().Roles(namespace).List(options)
			},
			WatchFunc: client.RbacV1().Roles(namespace).Watch,
		}
	})
}

func RoleBindingListerWatcher(client kubernetes.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.RbacV1().RoleBindings(namespace).List(options)
			},
			WatchFunc: client.RbacV1().RoleBindings(namespace).Watch,
		}
	})
}

func ConfigMapListerWatcher(client kubernetes.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.CoreV1().ConfigMaps(namespace).List(options)
			},
			WatchFunc: client.CoreV1().ConfigMaps(namespace).Watch,
		}
	})
}

func SecretListerWatcher(client kubernetes.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.CoreV1().Secrets(namespace).List(options)
			},
			WatchFunc: client.CoreV1().Secrets(namespace).Watch,
		}
	})
}

func ServiceListerWatcher(client kubernetes.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.CoreV1().Services(namespace).List(options)
			},
			WatchFunc: client.CoreV1().Services(namespace).Watch,
		}
	})
}

func ServiceAccountListerWatcher(client kubernetes.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.CoreV1().ServiceAccounts(namespace).List(options)
			},
			WatchFunc: client.CoreV1().ServiceAccounts(namespace).Watch,
		}
	})
}

func PodListerWatcher(client kubernetes.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.CoreV1().Pods(namespace).List(options)
			},
			WatchFunc: client.CoreV1().Pods(namespace).Watch,
		}
	})
}

func DeploymentListerWatcher(client kubernetes.Interface, namespaces ...string) cache.ListerWatcher {
	return mlw.MultiNamespaceListerWatcher(namespaces, func(namespace string) cache.ListerWatcher {
		return &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return client.AppsV1().Deployments(namespace).List(options)
			},
			WatchFunc: client.AppsV1().Deployments(namespace).Watch,
		}
	})
}
