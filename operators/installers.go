package alm

import (
	"fmt"

	"github.com/coreos-inc/operator-client/pkg/client"
	"k8s.io/api/extensions/v1beta1"
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

func (d *KubeDeployment) Install(ns string, rawDeployments interface{}) error {
	deploymentSpecs, ok := rawDeployments.([]v1beta1.DeploymentSpec)
	if !ok {
		return fmt.Errorf("coudn't cast deployment spec for install.deployments")
	}
	for _, spec := range deploymentSpecs {
		dep := v1beta1.Deployment{Spec: spec}
		dep.Namespace = ns
		_, err := d.client.CreateDeployment(&dep)
		return err
	}
	return nil
}
