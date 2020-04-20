package operatorclient

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
)

// CreateService creates the Service.
func (c *Client) CreateService(ig *v1.Service) (*v1.Service, error) {
	return c.CoreV1().Services(ig.GetNamespace()).Create(context.TODO(), ig, metav1.CreateOptions{})
}

// GetService returns the existing Service.
func (c *Client) GetService(namespace, name string) (*v1.Service, error) {
	return c.CoreV1().Services(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

// DeleteService deletes the Service.
func (c *Client) DeleteService(namespace, name string, options *metav1.DeleteOptions) error {
	return c.CoreV1().Services(namespace).Delete(context.TODO(), name, *options)
}

// UpdateService will update the given Service resource.
func (c *Client) UpdateService(service *v1.Service) (*v1.Service, error) {
	klog.V(4).Infof("[UPDATE Service]: %s", service.GetName())
	oldSa, err := c.GetService(service.GetNamespace(), service.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldSa, service)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for Service: %v", err)
	}
	return c.CoreV1().Services(service.GetNamespace()).Patch(context.TODO(), service.GetName(), types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
}
