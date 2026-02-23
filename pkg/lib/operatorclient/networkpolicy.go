package operatorclient

import (
	"context"
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
)

// CreateNetworkPolicy creates the NetworkPolicy.
func (c *Client) CreateNetworkPolicy(in *networkingv1.NetworkPolicy) (*networkingv1.NetworkPolicy, error) {
	createdNP, err := c.NetworkingV1().NetworkPolicies(in.GetNamespace()).Create(context.TODO(), in, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return c.UpdateNetworkPolicy(in)
	}
	return createdNP, err
}

// GetNetworkPolicy returns the existing NetworkPolicy.
func (c *Client) GetNetworkPolicy(namespace, name string) (*networkingv1.NetworkPolicy, error) {
	return c.NetworkingV1().NetworkPolicies(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

// DeleteNetworkPolicy deletes the NetworkPolicy.
func (c *Client) DeleteNetworkPolicy(namespace, name string, options *metav1.DeleteOptions) error {
	return c.NetworkingV1().NetworkPolicies(namespace).Delete(context.TODO(), name, *options)
}

// UpdateNetworkPolicy will update the given NetworkPolicy resource.
func (c *Client) UpdateNetworkPolicy(networkPolicy *networkingv1.NetworkPolicy) (*networkingv1.NetworkPolicy, error) {
	klog.V(4).Infof("[UPDATE NetworkPolicy]: %s", networkPolicy.GetName())
	oldNp, err := c.GetNetworkPolicy(networkPolicy.GetNamespace(), networkPolicy.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldNp, networkPolicy)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for NetworkPolicy: %v", err)
	}
	return c.NetworkingV1().NetworkPolicies(networkPolicy.GetNamespace()).Patch(context.TODO(), networkPolicy.GetName(), types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
}
