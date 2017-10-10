package install

import (
	"fmt"

	"github.com/coreos-inc/operator-client/pkg/client"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	InstallStrategyNameDeployment = "deployment"
)

var BlockOwnerDeletion = true
var Controller = false

// StrategyDetailsDeployment represents the parsed details of a Deployment
// InstallStrategy.
type StrategyDetailsDeployment struct {
	DeploymentSpecs []v1beta1.DeploymentSpec `json:"deployments"`
}

func (d *StrategyDetailsDeployment) Install(
	client client.Interface,
	owner metav1.ObjectMeta,
	ownerType metav1.TypeMeta,
) error {
	if err := checkInstalled(client, owner, len(d.DeploymentSpecs)); err != nil {
		return err
	}
	for _, spec := range d.DeploymentSpecs {
		ownerReferences := []metav1.OwnerReference{
			{
				APIVersion:         ownerType.APIVersion,
				Kind:               ownerType.Kind,
				Name:               owner.GetName(),
				UID:                owner.UID,
				Controller:         &Controller,
				BlockOwnerDeletion: &BlockOwnerDeletion,
			},
		}
		dep := v1beta1.Deployment{Spec: spec}
		dep.SetNamespace(owner.Namespace)
		dep.SetOwnerReferences(ownerReferences)
		dep.SetGenerateName(fmt.Sprintf("%s-", owner.Name))
		if dep.Labels == nil {
			dep.SetLabels(map[string]string{})
		}
		dep.Labels["alm-owned"] = "true"
		dep.Labels["alm-owner-name"] = owner.Name
		dep.Labels["alm-owner-namespace"] = owner.Namespace
		_, err := client.CreateDeployment(&dep)
		return err
	}
	return nil
}

func (d *StrategyDetailsDeployment) CheckInstalled(client client.Interface, owner metav1.ObjectMeta) (bool, error) {
	existingDeployments, err := client.ListDeploymentsWithLabels(
		owner.Namespace,
		map[string]string{
			"alm-owned":           "true",
			"alm-owner-name":      owner.Name,
			"alm-owner-namespace": owner.Namespace,
		},
	)
	if err != nil {
		return false, fmt.Errorf("couldn't query for existing deployments: %s", err)
	}
	if len(existingDeployments.Items) == len(d.DeploymentSpecs) {
		return true, nil
	}
	return false, nil
}
