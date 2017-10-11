package client

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

type ClusterServiceVersionInterface interface {
	UpdateCSV(csv *v1alpha1.ClusterServiceVersion) (result *v1alpha1.ClusterServiceVersion, err error)
}

type ClusterServiceVersionClient struct {
	*rest.RESTClient
}

var _ ClusterServiceVersionInterface = &ClusterServiceVersionClient{}

// NewClusterServiceVersionClient creates a client that can interact with the ClusterServiceVersion resource in k8s api
func NewClusterServiceVersionClient(kubeconfig string) (client *ClusterServiceVersionClient, err error) {
	config, err := getConfig(kubeconfig)
	if err != nil {
		return
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	config.GroupVersion = &v1alpha1.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: serializer.NewCodecFactory(scheme)}
	restClient, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, err
	}
	return &ClusterServiceVersionClient{restClient}, nil
}

func (c *ClusterServiceVersionClient) UpdateCSV(csv *v1alpha1.ClusterServiceVersion) (result *v1alpha1.ClusterServiceVersion, err error) {
	result = &v1alpha1.ClusterServiceVersion{}
	err = c.RESTClient.Put().Context(context.TODO()).
		Namespace(csv.Namespace).
		Resource("clusterserviceversion-v1s").
		Name(csv.Name).
		Body(csv).
		Do().
		Into(result)
	if err != nil {
		err = fmt.Errorf("failed to update CR status: %v", err)
	}
	return
}
