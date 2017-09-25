package install

import (
	"fmt"

	"github.com/coreos-inc/operator-client/pkg/client"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const InstallStrategyNameDeployment = "deployment"

// StrategyDetailsDeployment represents the parsed details of a Deployment
// InstallStrategy.
type StrategyDetailsDeployment struct {
	DeploymentSpecs []v1beta1.DeploymentSpec `json:"deployments"`
}

func (d *StrategyDetailsDeployment) Install(client client.Interface, owner metav1.ObjectMeta) error {
	if err := checkInstalled(client, owner, len(d.DeploymentSpecs)); err != nil {
		return err
	}
	for _, spec := range d.DeploymentSpecs {
		dep := v1beta1.Deployment{Spec: spec}
		dep.Namespace = owner.Namespace
		dep.GenerateName = fmt.Sprintf("%s-", owner.Name)
		if dep.Labels == nil {
			dep.Labels = map[string]string{}
		}
		dep.Labels["alm-owned"] = "true"
		dep.Labels["alm-owner-name"] = owner.Name
		dep.Labels["alm-owner-namespace"] = owner.Namespace
		_, err := client.CreateDeployment(&dep)
		return err
	}
	return nil
}

func checkInstalled(client client.Interface, owner metav1.ObjectMeta, expected int) error {
	existingDeployments, err := client.ListDeploymentsWithLabels(
		owner.Namespace,
		map[string]string{
			"alm-owned":           "true",
			"alm-owner-name":      owner.Name,
			"alm-owner-namespace": owner.Namespace,
		},
	)
	if err != nil {
		return fmt.Errorf("couldn't query for existing deployments: %s", err)
	}
	if len(existingDeployments.Items) == expected {
		return fmt.Errorf("deployments found for %s, skipping install: %v", owner.Name, existingDeployments)
	}
	return nil
}
