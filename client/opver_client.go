package client

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
)

// NewOperatorVersionClient creates a client that can interact with the OperatorVersion resource in k8s api
func NewOperatorVersionClient(kubeconfig string) (client *rest.RESTClient, err error) {
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
	return rest.RESTClientFor(config)
}
