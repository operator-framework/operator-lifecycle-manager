package operatorclient

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
)

// CreateConfigMap creates the ConfigMap.
func (c *Client) CreateConfigMap(ig *corev1.ConfigMap) (*corev1.ConfigMap, error) {
	return c.CoreV1().ConfigMaps(ig.GetNamespace()).Create(context.TODO(), ig, metav1.CreateOptions{})
}

// GetConfigMap returns the existing ConfigMap.
func (c *Client) GetConfigMap(namespace, name string) (*corev1.ConfigMap, error) {
	return c.CoreV1().ConfigMaps(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

// DeleteConfigMap deletes the ConfigMap.
func (c *Client) DeleteConfigMap(namespace, name string, options *metav1.DeleteOptions) error {
	return c.CoreV1().ConfigMaps(namespace).Delete(context.TODO(), name, *options)
}

// UpdateConfigMap will update the given ConfigMap resource.
func (c *Client) UpdateConfigMap(configmap *corev1.ConfigMap) (*corev1.ConfigMap, error) {
	klog.V(4).Infof("[UPDATE ConfigMap]: %s", configmap.GetName())
	oldSa, err := c.GetConfigMap(configmap.GetNamespace(), configmap.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldSa, configmap)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for ConfigMap: %v", err)
	}
	return c.CoreV1().ConfigMaps(configmap.GetNamespace()).Patch(context.TODO(), configmap.GetName(), types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
}
