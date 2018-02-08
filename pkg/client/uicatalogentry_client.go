//go:generate counterfeiter uicatalogentry_client.go UICatalogEntryInterface
package client

import (
	"context"
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
)

type UICatalogEntryInterface interface {
	UpdateEntry(csv *v1alpha1.UICatalogEntry) (*v1alpha1.UICatalogEntry, error)
	ListEntries(namespace string) (*v1alpha1.UICatalogEntryList, error)
	Delete(namespace, name string, options *metav1.DeleteOptions) error
}

type UICatalogEntryClient struct {
	*rest.RESTClient
}

var _ UICatalogEntryInterface = &UICatalogEntryClient{}

// NewUICatalogEntryClient creates a client that can interact with the UICatalogEntry resource in k8s api
func NewUICatalogEntryClient(kubeconfig string) (client *UICatalogEntryClient, err error) {
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
	return &UICatalogEntryClient{restClient}, nil
}

func (c *UICatalogEntryClient) UpdateEntry(in *v1alpha1.UICatalogEntry) (*v1alpha1.UICatalogEntry, error) {
	log.Debugf("UpdateEntry -- BEGIN %s", in.Name)
	result := &v1alpha1.UICatalogEntry{}

	err := c.RESTClient.Post().Context(context.TODO()).
		Namespace(in.Namespace).
		Resource(v1alpha1.UICatalogEntryCRDName).
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
		return nil, fmt.Errorf("failed to create or update UICatalogEntry: %s", err)
	}
	curr, err := c.getEntry(in.Namespace, in.Name)
	if err != nil {
		log.Errorf("UpdateEntry -- ERROR %s -- error fetching current entry: %s", in.Name, err)
		return nil, fmt.Errorf("failed to find then update UICatalogEntry: %s", err)
	}
	in.SetResourceVersion(curr.GetResourceVersion())
	err = c.RESTClient.Put().Context(context.TODO()).
		Namespace(in.Namespace).
		Resource(v1alpha1.UICatalogEntryCRDName).
		Name(in.Name).
		Body(in).
		Do().
		Into(result)

	if err != nil {
		log.Errorf("UpdateEntry -- ERROR %s -- error attempting to update entry: %s", in.Name, err)
		return nil, errors.New("failed to update UICatalogEntry: " + err.Error())
	}

	log.Debugf("UpdateEntry -- OK    %s -- updated exisiting entry", in.Name)
	return result, nil
}

func (c *UICatalogEntryClient) getEntry(namespace, serviceName string) (*v1alpha1.UICatalogEntry, error) {
	result := &v1alpha1.UICatalogEntry{}
	err := c.RESTClient.Get().Context(context.TODO()).
		Namespace(namespace).
		Resource(v1alpha1.UICatalogEntryCRDName).
		Name(serviceName).
		Do().
		Into(result)
	if err != nil {
		return nil, errors.New("failed to update UICatalogEntry status: " + err.Error())
	}
	return result, nil

}

func (c *UICatalogEntryClient) ListEntries(namespace string) (*v1alpha1.UICatalogEntryList, error) {
	result := &v1alpha1.UICatalogEntryList{}
	err := c.RESTClient.Get().
		Namespace(namespace).
		Resource(v1alpha1.UICatalogEntryCRDName).
		Do().
		Into(result)
	if err != nil {
		return nil, errors.New("failed to list UICatalogEntries: " + err.Error())
	}
	return result, nil
}

func (c *UICatalogEntryClient) Delete(namespace, name string, options *metav1.DeleteOptions) error {
	return c.RESTClient.Delete().
		Namespace(namespace).
		Resource(v1alpha1.UICatalogEntryCRDName).
		Name(name).
		Body(options).
		Do().
		Error()
}
