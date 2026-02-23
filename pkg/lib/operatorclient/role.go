package operatorclient

import (
	"context"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
)

// CreateRole creates the role or Updates if it already exists.
func (c *Client) CreateRole(r *rbacv1.Role) (*rbacv1.Role, error) {
	createdRole, err := c.RbacV1().Roles(r.GetNamespace()).Create(context.TODO(), r, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return c.UpdateRole(r)
	}
	return createdRole, err
}

// GetRole returns the existing role.
func (c *Client) GetRole(namespace, name string) (*rbacv1.Role, error) {
	return c.RbacV1().Roles(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

// DeleteRole deletes the role.
func (c *Client) DeleteRole(namespace, name string, options *metav1.DeleteOptions) error {
	return c.RbacV1().Roles(namespace).Delete(context.TODO(), name, *options)
}

// UpdateRole will update the given Role resource.
func (c *Client) UpdateRole(crb *rbacv1.Role) (*rbacv1.Role, error) {
	klog.V(4).Infof("[UPDATE Role]: %s", crb.GetName())
	oldCrb, err := c.GetRole(crb.GetNamespace(), crb.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldCrb, crb)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for Role: %v", err)
	}
	return c.RbacV1().Roles(crb.GetNamespace()).Patch(context.TODO(), crb.GetName(), types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
}
