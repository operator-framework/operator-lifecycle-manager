package operatorlister

import (
	"sync"

	"k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	rbacv1 "k8s.io/client-go/listers/rbac/v1"
)

type UnionRoleLister struct {
	roleListers map[string]rbacv1.RoleLister
	roleLock    sync.RWMutex
}

// List lists all Roles in the indexer.
func (rl *UnionRoleLister) List(selector labels.Selector) (ret []*v1.Role, err error) {
	rl.roleLock.RLock()
	defer rl.roleLock.RUnlock()

	var set map[types.UID]*v1.Role
	for _, dl := range rl.roleListers {
		roles, err := dl.List(selector)
		if err != nil {
			return nil, err
		}

		for _, role := range roles {
			set[role.GetUID()] = role
		}
	}

	for _, role := range set {
		ret = append(ret, role)
	}

	return
}

// Roles returns an object that can list and get Roles.
func (rl *UnionRoleLister) Roles(namespace string) rbacv1.RoleNamespaceLister {
	rl.roleLock.RLock()
	defer rl.roleLock.RUnlock()

	// Check for specific namespace listers
	if dl, ok := rl.roleListers[namespace]; ok {
		return dl.Roles(namespace)
	}

	// Check for any namespace-all listers
	if dl, ok := rl.roleListers[metav1.NamespaceAll]; ok {
		return dl.Roles(namespace)
	}

	return nil
}

func (rl *UnionRoleLister) RegisterRoleLister(namespace string, lister rbacv1.RoleLister) {
	rl.roleLock.Lock()
	defer rl.roleLock.Unlock()

	if rl.roleListers == nil {
		rl.roleListers = make(map[string]rbacv1.RoleLister)
	}
	rl.roleListers[namespace] = lister
}

func (l *rbacV1Lister) RegisterRoleLister(namespace string, lister rbacv1.RoleLister) {
	l.roleLister.RegisterRoleLister(namespace, lister)
}

func (l *rbacV1Lister) RoleLister() rbacv1.RoleLister {
	return l.roleLister
}
