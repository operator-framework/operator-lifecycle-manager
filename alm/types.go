package alm

import (
	"context"
	"fmt"
	"k8s.io/client-go/rest"
)

type OperatorInstaller interface {
	Install(ctx context.Context, ns string, data interface{}) error
}

type KubeManifest struct {
	config *rest.Config
	//client *rest.RESTClient
	//	resource string // resource name for operators
}

// direct passthrough to k8s api - note data
// func (k *KubeManifest) Install(ctx context, ns string, data interface{}) error {
// 	return k.client.Post().Context(ctx).
// 		Namespace(ns).
// 		Resource(k.resource).
// 		Body(data).
// 		Do()
// }

type MockInstall struct {
	Name string
}

func (m *MockInstall) Install(ctx context.Context, ns string, data []byte) error {
	fmt.Printf("INSTALL %s: ctx=%+v ns=%s data=%s\n", m.Name, ctx, ns, data)
	return nil
}
