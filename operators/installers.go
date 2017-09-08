package alm

import (
	"encoding/json"
	"fmt"
	"github.com/coreos-inc/operator-client/pkg/client"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	KubeDeploymentMethodName = "deployment"
)

type KubeDeployment struct {
	client client.Interface
}

func (d *KubeDeployment) Method() string {
	return KubeDeploymentMethodName
}

func (d *KubeDeployment) Install(ns string, spec *unstructured.Unstructured) error {
	dep, err := deploymentFromUnstructured(spec)
	if err != nil {
		return fmt.Errorf("expected spec to decode into *extensions.Deployment, got %T", dep)
	}
	dep.SetNamespace(ns)
	_, err = d.client.CreateDeployment(dep)
	return err
}

func deploymentFromUnstructured(d *unstructured.Unstructured) (*v1beta1.Deployment, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("error marshaling unstructured resource: %v", err)
	}
	var dep v1beta1.Deployment
	if err := json.Unmarshal(data, &dep); err != nil {
		return nil, fmt.Errorf("error unmarshaling marshaled resource: %v", err)
	}
	return &dep, nil
}
