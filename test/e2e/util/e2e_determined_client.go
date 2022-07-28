package util

import (
	"context"

	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

// DeterminedE2EClient wraps E2eClient calls in an Eventually assertion to keep trying
// or bail after some time if unsuccessful
type DeterminedE2EClient struct {
	*E2EKubeClient
}

func NewDeterminedClient(e2eKubeClient *E2EKubeClient) *DeterminedE2EClient {
	return &DeterminedE2EClient{
		e2eKubeClient,
	}
}

func (m *DeterminedE2EClient) Create(context context.Context, obj k8scontrollerclient.Object, options ...k8scontrollerclient.CreateOption) error {
	m.keepTrying(func() error {
		err := m.E2EKubeClient.Create(context, obj, options...)
		return err
	})
	return nil
}

func (m *DeterminedE2EClient) Update(context context.Context, obj k8scontrollerclient.Object, options ...k8scontrollerclient.UpdateOption) error {
	m.keepTrying(func() error {
		return m.E2EKubeClient.Update(context, obj, options...)
	})
	return nil
}

func (m *DeterminedE2EClient) Delete(context context.Context, obj k8scontrollerclient.Object, options ...k8scontrollerclient.DeleteOption) error {
	m.keepTrying(func() error {
		return m.E2EKubeClient.Delete(context, obj, options...)
	})
	return nil
}

func (m *DeterminedE2EClient) Patch(context context.Context, obj k8scontrollerclient.Object, patch k8scontrollerclient.Patch, options ...k8scontrollerclient.PatchOption) error {
	m.keepTrying(func() error {
		return m.E2EKubeClient.Patch(context, obj, patch, options...)
	})
	return nil
}

func (m *DeterminedE2EClient) keepTrying(fn func() error) {
	Eventually(fn).Should(Succeed())
}
