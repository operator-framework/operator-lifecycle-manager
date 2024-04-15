package operatorclient

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
)

// CreateSecret creates the Secret or Updates if it already exists.
func (c *Client) CreateSecret(ig *v1.Secret) (*v1.Secret, error) {
	createdSecret, err := c.CoreV1().Secrets(ig.GetNamespace()).Create(context.TODO(), ig, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return c.UpdateSecret(ig)
	}
	return createdSecret, err
}

// GetSecret returns the existing Secret.
func (c *Client) GetSecret(namespace, name string) (*v1.Secret, error) {
	return c.CoreV1().Secrets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

// DeleteSecret deletes the Secret.
func (c *Client) DeleteSecret(namespace, name string, options *metav1.DeleteOptions) error {
	return c.CoreV1().Secrets(namespace).Delete(context.TODO(), name, *options)
}

// UpdateSecret will update the given Secret resource.
func (c *Client) UpdateSecret(secret *v1.Secret) (*v1.Secret, error) {
	klog.V(4).Infof("[UPDATE Secret]: %s", secret.GetName())
	oldSa, err := c.GetSecret(secret.GetNamespace(), secret.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldSa, secret)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for Secret: %v", err)
	}
	return c.CoreV1().Secrets(secret.GetNamespace()).Patch(context.TODO(), secret.GetName(), types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
}
