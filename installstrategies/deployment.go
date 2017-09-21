package installstrategies

import (
	"fmt"

	"github.com/coreos-inc/operator-client/pkg/client"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KubeDeploymentMethodName = "deployment"
)

type KubeDeployment struct {
	client client.Interface
}

func NewKubeDeployment(client client.Interface) *KubeDeployment {
	return &KubeDeployment{client: client}
}

func (d *KubeDeployment) Method() string {
	return KubeDeploymentMethodName
}

func (d *KubeDeployment) Install(owner metav1.ObjectMeta, deploymentSpecs []v1beta1.DeploymentSpec) error {
	for _, spec := range deploymentSpecs {
		dep := v1beta1.Deployment{Spec: spec}
		dep.Namespace = owner.Namespace
		dep.GenerateName = fmt.Sprintf("%s-", owner.Name)
		if dep.Labels == nil {
			dep.Labels = map[string]string{}
		}
		dep.Labels["alm-owned"] = "true"
		dep.Labels["alm-owner-name"] = owner.Name
		dep.Labels["alm-owner-namespace"] = owner.Namespace
		_, err := d.client.CreateDeployment(&dep)
		return err
	}
	return nil
}
