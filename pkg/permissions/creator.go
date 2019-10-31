package permissions

import (
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
)

type Creator interface {
	FromOperatorPermissions(namespace string, permissions map[string]*resolver.OperatorPermissions) error
}

var _ Creator = PermissionCreator{}

type PermissionCreator struct {
	client kubernetes.Interface
}

func NewPermissionCreator(client kubernetes.Interface) *PermissionCreator {
	return &PermissionCreator{client: client}
}

// TODO: should these attempt to update?
func (p PermissionCreator) FromOperatorPermissions(namespace string, permissions map[string]*resolver.OperatorPermissions) error {
	for _, perms := range permissions {
		if _, err := p.client.CoreV1().ServiceAccounts(namespace).Create(perms.ServiceAccount); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}

		for _, role := range perms.Roles {
			if _, err := p.client.RbacV1().Roles(namespace).Create(role); err != nil && !errors.IsAlreadyExists(err) {
				return err
			}
		}
		for _, role := range perms.ClusterRoles {
			if _, err := p.client.RbacV1().ClusterRoles().Create(role); err != nil && !errors.IsAlreadyExists(err) {
				return err
			}
		}
		for _, rb := range perms.RoleBindings {
			if _, err := p.client.RbacV1().RoleBindings(namespace).Create(rb); err != nil && !errors.IsAlreadyExists(err) {
				return err
			}
		}
		for _, crb := range perms.ClusterRoleBindings {
			if _, err := p.client.RbacV1().ClusterRoleBindings().Create(crb); err != nil && !errors.IsAlreadyExists(err) {
				return err
			}
		}
	}
	return nil
}
