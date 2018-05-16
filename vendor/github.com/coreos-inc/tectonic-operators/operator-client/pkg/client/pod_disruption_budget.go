package client

import (
	"fmt"

	"github.com/golang/glog"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// CreatePodDisruptionBudget creates the PodDisruptionBudget.
func (c *Client) CreatePodDisruptionBudget(pdb *policyv1beta1.PodDisruptionBudget) (*policyv1beta1.PodDisruptionBudget, error) {
	return c.PolicyV1beta1().PodDisruptionBudgets(pdb.GetNamespace()).Create(pdb)
}

// GetPodDisruptionBudget returns the existing PodDisruptionBudget.
func (c *Client) GetPodDisruptionBudget(namespace, name string) (*policyv1beta1.PodDisruptionBudget, error) {
	return c.PolicyV1beta1().PodDisruptionBudgets(namespace).Get(name, metav1.GetOptions{})
}

// DeletePodDisruptionBudget deletes the PodDisruptionBudget.
func (c *Client) DeletePodDisruptionBudget(namespace, name string, options *metav1.DeleteOptions) error {
	return c.PolicyV1beta1().PodDisruptionBudgets(namespace).Delete(name, options)
}

// UpdatePodDisruptionBudget will update the given PodDisruptionBudget resource.
func (c *Client) UpdatePodDisruptionBudget(pdb *policyv1beta1.PodDisruptionBudget) (*policyv1beta1.PodDisruptionBudget, error) {
	glog.V(4).Infof("[UPDATE PodDisruptionBudget]: %s", pdb.GetName())
	oldPdb, err := c.GetPodDisruptionBudget(pdb.GetNamespace(), pdb.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldPdb, pdb)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for PodDisruptionBudget: %v", err)
	}
	return c.PolicyV1beta1().PodDisruptionBudgets(pdb.GetNamespace()).Patch(pdb.GetName(), types.StrategicMergePatchType, patchBytes)
}
