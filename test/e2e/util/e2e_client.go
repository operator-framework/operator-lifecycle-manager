package util

import (
	"context"

	"github.com/onsi/ginkgo"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
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
	for {
		obj, ok := m.createdResources.DequeueTail()

		if !ok {
			break
		}

		if err := m.Delete(context.TODO(), obj); err != nil && !k8serror.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (m *E2EKubeClient) annotateTestResource(obj k8scontrollerclient.Object) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[E2ETestNameTag] = ginkgo.CurrentGinkgoTestDescription().FullTestText
	obj.SetAnnotations(annotations)
}
