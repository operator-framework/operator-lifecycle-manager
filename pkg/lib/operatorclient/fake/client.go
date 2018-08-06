package fake

import (
	apiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	extfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

// Interface assertion.
var _ operatorclient.ClientInterface = &Client{}

// Client is a kubernetes client that can talk to the API server.
type Client struct {
	*k8sfake.Clientset
	extClientset *extfake.Clientset
}

// NewClient creates a kubernetes client or bails out on on failures.
func NewClient(kClient *k8sfake.Clientset, eClient *extfake.Clientset) operatorclient.ClientInterface {
	return &Client{kClient, eClient}
}

// KubernetesInterface returns the Kubernetes interface.
func (c *Client) KubernetesInterface() kubernetes.Interface {
	return c.Clientset
}

// ApiextensionsV1beta1Interface returns the API extention interface.
func (c *Client) ApiextensionsV1beta1Interface() apiextensions.Interface {
	return c.extClientset
}
