package operatorlister

import (
	"sync"

	"k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	rbacv1 "k8s.io/client-go/listers/rbac/v1"
)

type UnionRoleBindingLister struct {
	roleBindingListers map[string]rbacv1.RoleBindingLister
	roleBindingLock    sync.RWMutex
}

// List lists all RoleBindings in the indexer.
func (rbl *UnionRoleBindingLister) List(selector labels.Selector) (ret []*v1.RoleBinding, err error) {
	rbl.roleBindingLock.RLock()
	defer rbl.roleBindingLock.RUnlock()

	var set map[types.UID]*v1.RoleBinding
	for _, dl := range rbl.roleBindingListers {
		roleBindings, err := dl.List(selector)
		if err != nil {
			return nil, err
		}

		for _, roleBinding := range roleBindings {
			set[roleBinding.GetUID()] = roleBinding
		}
	}

	for _, roleBinding := range set {
		ret = append(ret, roleBinding)
	}

	return
}

// RoleBindings returns an object that can list and get RoleBindings.
func (rbl *UnionRoleBindingLister) RoleBindings(namespace string) rbacv1.RoleBindingNamespaceLister {
	rbl.roleBindingLock.RLock()
	defer rbl.roleBindingLock.RUnlock()

	// Check for specific namespace listers
	if dl, ok := rbl.roleBindingListers[namespace]; ok {
		return dl.RoleBindings(namespace)
	}

	// Check for any namespace-all listers
	if dl, ok := rbl.roleBindingListers[metav1.NamespaceAll]; ok {
		return dl.RoleBindings(namespace)
	}

	return nil
}

func (rbl *UnionRoleBindingLister) RegisterRoleBindingLister(namespace string, lister rbacv1.RoleBindingLister) {
	rbl.roleBindingLock.Lock()
	defer rbl.roleBindingLock.Unlock()

	if rbl.roleBindingListers == nil {
		rbl.roleBindingListers = make(map[string]rbacv1.RoleBindingLister)
	}
	rbl.roleBindingListers[namespace] = lister
}

func (l *rbacV1Lister) RegisterRoleBindingLister(namespace string, lister rbacv1.RoleBindingLister) {
	l.roleBindingLister.RegisterRoleBindingLister(namespace, lister)
}

func (l *rbacV1Lister) RoleBindingLister() rbacv1.RoleBindingLister {
	return l.roleBindingLister
}
