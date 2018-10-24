package operatorlister

import (
	"sync"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corev1 "k8s.io/client-go/listers/core/v1"
)

type UnionServiceAccountLister struct {
	serviceAccountListers map[string]corev1.ServiceAccountLister
	serviceAccountLock    sync.RWMutex
}

// List lists all ServiceAccounts in the indexer.
func (usl *UnionServiceAccountLister) List(selector labels.Selector) (ret []*v1.ServiceAccount, err error) {
	usl.serviceAccountLock.RLock()
	defer usl.serviceAccountLock.RUnlock()

	var set map[types.UID]*v1.ServiceAccount
	for _, sl := range usl.serviceAccountListers {
		serviceAccounts, err := sl.List(selector)
		if err != nil {
			return nil, err
		}

		for _, serviceAccount := range serviceAccounts {
			set[serviceAccount.GetUID()] = serviceAccount
		}
	}

	for _, serviceAccount := range set {
		ret = append(ret, serviceAccount)
	}

	return
}

// ServiceAccounts returns an object that can list and get ServiceAccounts.
func (usl *UnionServiceAccountLister) ServiceAccounts(namespace string) corev1.ServiceAccountNamespaceLister {
	usl.serviceAccountLock.RLock()
	defer usl.serviceAccountLock.RUnlock()

	// Check for specific namespace listers
	if sl, ok := usl.serviceAccountListers[namespace]; ok {
		return sl.ServiceAccounts(namespace)
	}

	// Check for any namespace-all listers
	if sl, ok := usl.serviceAccountListers[metav1.NamespaceAll]; ok {
		return sl.ServiceAccounts(namespace)
	}

	// TODO: Return dummy ServiceAccountNamespaceLister
	return nil
}

func (usl *UnionServiceAccountLister) RegisterServiceAccountLister(namespace string, lister corev1.ServiceAccountLister) {
	usl.serviceAccountLock.Lock()
	defer usl.serviceAccountLock.Unlock()

	if usl.serviceAccountListers == nil {
		usl.serviceAccountListers = make(map[string]corev1.ServiceAccountLister)
	}
	usl.serviceAccountListers[namespace] = lister
}

func (l *coreV1Lister) RegisterServiceAccountLister(namespace string, lister corev1.ServiceAccountLister) {
	l.serviceAccountLister.RegisterServiceAccountLister(namespace, lister)
}

func (l *coreV1Lister) ServiceAccountLister() corev1.ServiceAccountLister {
	return l.serviceAccountLister
}
