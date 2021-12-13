package client

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"k8s.io/client-go/rest"
)

// NewClient creates a client that can interact with OLM resources in k8s api
func NewClient(config *rest.Config) (client versioned.Interface, err error) {
	return versioned.NewForConfig(config)
}
