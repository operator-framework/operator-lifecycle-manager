package client

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/pkg/apis/catalogsource/v1alpha1"
)

type CatalogSourceInterface interface {
	GetCS(namespace, name string) (*v1alpha1.CatalogSource, error)
	UpdateCS(cs *v1alpha1.CatalogSource) (result *v1alpha1.CatalogSource, err error)
	CreateCS(cs *v1alpha1.CatalogSource) (err error)
}

type CatalogSourceClient struct {
	*rest.RESTClient
}

var _ CatalogSourceInterface = &CatalogSourceClient{}

// NewCatalogSourceClient creates a client that can interact with the CatalogSource resource in k8s api
func NewCatalogSourceClient(kubeconfig string) (client *CatalogSourceClient, err error) {
	var config *rest.Config
	config, err = getConfig(kubeconfig)
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
		return
	}
	return &CatalogSourceClient{restClient}, nil
}

func (c *CatalogSourceClient) GetCS(namespace, name string) (*v1alpha1.CatalogSource, error) {
	result := &v1alpha1.CatalogSource{}
	err := c.RESTClient.Get().Context(context.TODO()).
		Namespace(namespace).
		Resource(v1alpha1.CatalogSourceCRDName).
		Name(name).
		Do().
		Into(result)
	if err != nil {
		err = errors.New("failed to get CatalogSource: " + err.Error())
	}
	return result, nil
}

func (c *CatalogSourceClient) UpdateCS(in *v1alpha1.CatalogSource) (result *v1alpha1.CatalogSource, err error) {
	result = &v1alpha1.CatalogSource{}
	err = c.RESTClient.Put().Context(context.TODO()).
		Namespace(in.Namespace).
		Resource(v1alpha1.CatalogSourceCRDName).
		Name(in.Name).
		Body(in).
		Do().
		Into(result)
	if err != nil {
		err = errors.New("failed to update CR status: " + err.Error())
	}
	return
}

func (c *CatalogSourceClient) CreateCS(cs *v1alpha1.CatalogSource) error {
	out := &v1alpha1.CatalogSource{}
	return c.RESTClient.
		Post().
		Context(context.TODO()).
		Namespace(cs.Namespace).
		Resource(v1alpha1.CatalogSourceCRDName).
		Name(cs.Name).
		Body(cs).
		Do().
		Into(out)
}
