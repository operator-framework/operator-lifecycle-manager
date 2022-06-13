package util

import (
	"context"
	"strings"

	"github.com/onsi/ginkgo/v2"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	E2ETestNameTag = "e2e.testName"
)

type E2EKubeClient struct {
	k8scontrollerclient.Client
	createdResources *ResourceQueue
}

func NewK8sResourceManager(client k8scontrollerclient.Client) *E2EKubeClient {
	return &E2EKubeClient{
		Client:           client,
		createdResources: NewResourceQueue(),
	}
}

func (m *E2EKubeClient) Create(context context.Context, obj k8scontrollerclient.Object, options ...k8scontrollerclient.CreateOption) error {
	m.annotateTestResource(obj)
	if err := m.Client.Create(context, obj, options...); err != nil {
		return err
	}
	m.createdResources.EnqueueIgnoreExisting(obj)
	return nil
}

func (m *E2EKubeClient) Update(context context.Context, obj k8scontrollerclient.Object, options ...k8scontrollerclient.UpdateOption) error {
	m.annotateTestResource(obj)
	if err := m.Client.Update(context, obj, options...); err != nil {
		return err
	}
	m.createdResources.EnqueueIgnoreExisting(obj)
	return nil
}

func (m *E2EKubeClient) Delete(context context.Context, obj k8scontrollerclient.Object, options ...k8scontrollerclient.DeleteOption) error {
	if err := m.Client.Delete(context, obj, options...); err != nil {
		return err
	}
	m.createdResources.RemoveIgnoreNotFound(obj)
	return nil
}

func (m *E2EKubeClient) Reset() error {
	Logf("resetting e2e kube client")
	for {
		obj, ok := m.createdResources.DequeueTail()

		if !ok {
			break
		}

		namespace := obj.GetNamespace()
		if namespace == "" {
			namespace = "<global>"
		}

		Logf("deleting %s/%s", namespace, obj.GetName())
		if err := k8scontrollerclient.IgnoreNotFound(m.Delete(context.Background(), obj)); err != nil {
			Logf("error deleting object %s/%s: %s", namespace, obj.GetName(), obj)
			return err
		}
	}
	return m.GarbageCollectCRDs()
}

// GarbageCollectCRDs deletes any CRD with a label like operatorframework.io/installed-alongside-*
// these are the result of operator installations by olm and tent to be left behind after an e2e test
func (m *E2EKubeClient) GarbageCollectCRDs() error {
	Logf("garbage collecting CRDs")
	const operatorFrameworkAnnotation = "operatorframework.io/installed-alongside-"

	crds := &extensionsv1.CustomResourceDefinitionList{}
	err := m.Client.List(context.Background(), crds)
	if err != nil {
		return err
	}

	for _, crd := range crds.Items {
		for key, _ := range crd.Annotations {
			if strings.HasPrefix(key, operatorFrameworkAnnotation) {
				Logf("deleting crd %s", crd.GetName())
				if err := m.Client.Delete(context.Background(), &crd); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (m *E2EKubeClient) annotateTestResource(obj k8scontrollerclient.Object) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[E2ETestNameTag] = ginkgo.CurrentSpecReport().FullText()
	obj.SetAnnotations(annotations)
}
