//go:generate counterfeiter subscription_client.go SubscriptionClientInterface
package client

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/pkg/apis/subscription/v1alpha1"
)

type SubscriptionClientInterface interface {
	CreateSubscription(*v1alpha1.Subscription) (*v1alpha1.Subscription, error)
	UpdateSubscription(*v1alpha1.Subscription) (*v1alpha1.Subscription, error)
}

type SubscriptionClient struct {
	*rest.RESTClient
}

var _ SubscriptionClientInterface = &SubscriptionClient{}

// NewCatalogClient creates a client that can interact with the Catalog resource in k8s api
func NewSubscriptionClient(kubeconfig string) (*SubscriptionClient, error) {
	config, err := getConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	config.GroupVersion = &v1alpha1.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: serializer.NewCodecFactory(scheme)}
	client, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, err
	}
	return &SubscriptionClient{client}, nil
}

func (c *SubscriptionClient) CreateSubscription(in *v1alpha1.Subscription) (*v1alpha1.Subscription, error) {
	out := &v1alpha1.Subscription{}
	err := c.RESTClient.
		Post().
		Context(context.TODO()).
		Namespace(in.Namespace).
		Resource(v1alpha1.SubscriptionCRDName).
		Name(in.Name).
		Body(in).
		Do().
		Into(out)
	if err != nil {
		return nil, fmt.Errorf("failed to create Subscription: %v", err)
	}
	return out, nil
}

func (c *SubscriptionClient) UpdateSubscription(in *v1alpha1.Subscription) (*v1alpha1.Subscription, error) {
	out := &v1alpha1.Subscription{}
	err := c.RESTClient.
		Put().
		Context(context.TODO()).
		Namespace(in.Namespace).
		Resource(v1alpha1.SubscriptionCRDName).
		Name(in.Name).
		Body(in).
		Do().
		Into(out)
	if err != nil {
		return nil, fmt.Errorf("failed to update Subscription: %v", err)
	}
	return out, nil
}
