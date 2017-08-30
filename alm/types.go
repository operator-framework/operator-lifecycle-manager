package alm

import (
	"context"
	"fmt"
	"k8s.io/client-go/rest"
)

type OperatorInstaller interface {
	Install(ctx context, ns string, data interface{}) error
}

type KubeManifest struct {
	client   *rest.RESTClient
	resource string // resource name for operators
}

// direct passthrough to k8s api - note data
func (k *KubeManifest) Install(ctx context, ns string, data interface{}) error {
	return k.client.Post().Context(ctx).
		Namespace(ns).
		Resource(k.resource).
		Body(data).
		Do()
}

type MockInstall struct {
}

func (m *MockInstall) Install(ctx context, ns string, data []byte) error {

}
