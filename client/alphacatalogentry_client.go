package client

import (
	"context"
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/apis/alphacatalogentry/v1alpha1"
)

type AlphaCatalogEntryInterface interface {
	UpdateEntry(csv *v1alpha1.AlphaCatalogEntry) (*v1alpha1.AlphaCatalogEntry, error)
}

type AlphaCatalogEntryClient struct {
	*rest.RESTClient
}

var _ AlphaCatalogEntryInterface = &AlphaCatalogEntryClient{}

// NewAlphaCatalogEntryClient creates a client that can interact with the AlphaCatalogEntry resource in k8s api
func NewAlphaCatalogEntryClient(kubeconfig string) (client *AlphaCatalogEntryClient, err error) {
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
	return &AlphaCatalogEntryClient{restClient}, nil
}

func (c *AlphaCatalogEntryClient) UpdateEntry(in *v1alpha1.AlphaCatalogEntry) (*v1alpha1.AlphaCatalogEntry, error) {
	log.Debugf("UpdateEntry -- BEGIN %s", in.Name)
	result := &v1alpha1.AlphaCatalogEntry{}

	err := c.RESTClient.Post().Context(context.TODO()).
		Namespace(in.Namespace).
		Resource(v1alpha1.AlphaCatalogEntryCRDName).
		Name(in.Name).
		Body(in).
		Do().
		Into(result)

	if err == nil {
		log.Debugf("UpdateEntry -- OK    %s -- created entry", in.Name)
		return result, nil
	}

	if !k8serrors.IsAlreadyExists(err) {
		log.Errorf("UpdateEntry -- ERROR %s -- error attempting to create entry: %s", in.Name, err)
		return nil, fmt.Errorf("failed to create or update AlphaCatalogEntry: %s", err)
	}
	curr, err := c.getEntry(in.Namespace, in.Name)
	if err != nil {
		log.Errorf("UpdateEntry -- ERROR %s -- error fetching current entry: %s", in.Name, err)
		return nil, fmt.Errorf("failed to find then update AlphaCatalogEntry: %s", err)
	}
	in.SetResourceVersion(curr.GetResourceVersion())
	err = c.RESTClient.Put().Context(context.TODO()).
		Namespace(in.Namespace).
		Resource(v1alpha1.AlphaCatalogEntryCRDName).
		Name(in.Name).
		Body(in).
		Do().
		Into(result)

	if err != nil {
		log.Errorf("UpdateEntry -- ERROR %s -- error attempting to update entry: %s", in.Name, err)
		return nil, errors.New("failed to update AlphaCatalogEntry: " + err.Error())
	}

	log.Debugf("UpdateEntry -- OK    %s -- updated exisiting entry", in.Name)
	return result, nil
}

func (c *AlphaCatalogEntryClient) getEntry(namespace, serviceName string) (*v1alpha1.AlphaCatalogEntry, error) {
	result := &v1alpha1.AlphaCatalogEntry{}
	err := c.RESTClient.Get().Context(context.TODO()).
		Namespace(namespace).
		Resource(v1alpha1.AlphaCatalogEntryCRDName).
		Name(serviceName).
		Do().
		Into(result)
	if err != nil {
		return nil, errors.New("failed to update CR status: " + err.Error())
	}
	return result, nil

}
