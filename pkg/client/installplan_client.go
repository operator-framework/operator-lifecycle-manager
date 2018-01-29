//go:generate counterfeiter installplan_client.go InstallPlanInterface
package client

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
)

type InstallPlanInterface interface {
	UpdateInstallPlan(*v1alpha1.InstallPlan) (*v1alpha1.InstallPlan, error)
	CreateInstallPlan(*v1alpha1.InstallPlan) (*v1alpha1.InstallPlan, error)
	GetInstallPlanByName(namespace string, name string) (*v1alpha1.InstallPlan, error)
}

type InstallPlanClient struct {
	*rest.RESTClient
}

var _ InstallPlanInterface = &InstallPlanClient{}

// NewInstallPlanClient creates a client that can interact with the InstallPlan
// resource using the Kubernetes API.
func NewInstallPlanClient(kubeconfigPath string) (client *InstallPlanClient, err error) {
	var config *rest.Config
	config, err = getConfig(kubeconfigPath)
	if err != nil {
		return
	}

	scheme := runtime.NewScheme()
	if err = v1alpha1.AddToScheme(scheme); err != nil {
		return
	}

	config.GroupVersion = &v1alpha1.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: serializer.NewCodecFactory(scheme)}

	var restClient *rest.RESTClient
	restClient, err = rest.RESTClientFor(config)
	if err != nil {
		return nil, err
	}

	return &InstallPlanClient{restClient}, nil
}

func (c *InstallPlanClient) UpdateInstallPlan(in *v1alpha1.InstallPlan) (out *v1alpha1.InstallPlan, err error) {
	out = &v1alpha1.InstallPlan{}
	if err = c.RESTClient.
		Put().
		Context(context.TODO()).
		Namespace(in.Namespace).
		Resource("installplan-v1s").
		Name(in.Name).
		Body(in).
		Do().
		Into(out); err != nil {
		err = fmt.Errorf("failed to update CR status: %v", err)
	}

	return
}

func (c *InstallPlanClient) CreateInstallPlan(in *v1alpha1.InstallPlan) (*v1alpha1.InstallPlan, error) {
	out := &v1alpha1.InstallPlan{}
	err := c.RESTClient.
		Post().
		Context(context.TODO()).
		Namespace(in.Namespace).
		Resource(v1alpha1.InstallPlanCRDName).
		Name(in.Name).
		Body(in).
		Do().
		Into(out)
	if err != nil {
		return nil, fmt.Errorf("failed to create InstallPlan: %v", err)
	}
	return out, nil
}

func (c *InstallPlanClient) GetInstallPlanByName(namespace, name string) (*v1alpha1.InstallPlan, error) {
	out := &v1alpha1.InstallPlan{}
	err := c.RESTClient.
		Get().
		Context(context.TODO()).
		Namespace(namespace).
		Resource(v1alpha1.InstallPlanCRDName).
		Name(name).
		Do().
		Into(out)
	return out, err
}
