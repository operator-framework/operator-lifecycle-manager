package client

import (
	"github.com/golang/glog"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Interface assertion.
var _ Interface = &Client{}

// Client is a kubernetes client that can talk to the API server.
type Client struct {
	config *rest.Config
	*kubernetes.Clientset
	extClientset *apiextensions.Clientset
}

// NewClient creates a kubernetes client or bails out on on failures.
func NewClient(kubeconfig string) Interface {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		glog.V(4).Infof("Loading kube client config from path %q", kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		glog.V(4).Infof("Using in-cluster kube client config")
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		glog.Fatalf("Cannot load config for REST client: %v", err)
	}

	return &Client{config, kubernetes.NewForConfigOrDie(config), apiextensions.NewForConfigOrDie(config)}
}

// KubernetesInterface returns the Kubernetes interface.
func (c *Client) KubernetesInterface() kubernetes.Interface {
	return c.Clientset
}

// ApiextensionsV1beta1Interface returns the API extention interface.
func (c *Client) ApiextensionsV1beta1Interface() apiextensions.Interface {
	return c.extClientset
}

// ImpersonatedClientForServiceAccount creates a client that impersonates a serviceaccount based on the current client
func (c *Client) ImpersonatedClientForServiceAccount(serviceAccountName string, namespace string) (Interface, error) {
	impersonatedConfig := CopyConfig(c.config)

	impersonatedConfig.Impersonate = rest.ImpersonationConfig{
		UserName: MakeUsername(namespace, serviceAccountName),
		Groups:   MakeGroupNames(namespace),
	}
	impersonatedKubernetesClient, err := kubernetes.NewForConfig(impersonatedConfig)
	if err != nil {
		return nil, err
	}
	impersonateExtensionClient, err := apiextensions.NewForConfig(impersonatedConfig)
	if err != nil {
		return nil, err
	}
	return &Client{
		impersonatedConfig,
		impersonatedKubernetesClient,
		impersonateExtensionClient,
	}, nil
}
