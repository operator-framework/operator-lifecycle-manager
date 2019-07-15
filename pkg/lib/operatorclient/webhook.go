package operatorclient

import (
	"fmt"

	"github.com/golang/glog"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// CreateValidatingWebhook creates the validating webhook
func (c *Client) CreateValidatingWebhook(hook *admissionregistrationv1beta1.ValidatingWebhookConfiguration) (*admissionregistrationv1beta1.ValidatingWebhookConfiguration, error) {
	return c.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations().Create(hook)
}

// CreateMutatingWebhook creates the validating webhook
func (c *Client) CreateMutatingWebhook(hook *admissionregistrationv1beta1.MutatingWebhookConfiguration) (*admissionregistrationv1beta1.MutatingWebhookConfiguration, error) {
	return c.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Create(hook)
}

// GetValidatingWebhook returns the validating webhook.
func (c *Client) GetValidatingWebhook(name string) (*admissionregistrationv1beta1.ValidatingWebhookConfiguration, error) {
	return c.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations().Get(name, metav1.GetOptions{})
}

// GetMutatingWebhook returns the mutating webhook.
func (c *Client) GetMutatingWebhook(name string) (*admissionregistrationv1beta1.MutatingWebhookConfiguration, error) {
	return c.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Get(name, metav1.GetOptions{})
}

// DeleteMutatingWebhook deletes the mutating webhook.
func (c *Client) DeleteMutatingWebhook(name string, options *metav1.DeleteOptions) error {
	return c.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Delete(name, options)
}

// DeleteValidatingWebhook deletes the validating webhook.
func (c *Client) DeleteValidatingWebhook(name string, options *metav1.DeleteOptions) error {
	return c.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations().Delete(name, options)
}

// UpdateMutatingWebhook will update the given mutating webhook resource.
func (c *Client) UpdateMutatingWebhook(hook *admissionregistrationv1beta1.MutatingWebhookConfiguration) (*admissionregistrationv1beta1.MutatingWebhookConfiguration, error) {
	glog.V(4).Infof("[UPDATE MutatingWebhook]: %s", hook.GetName())
	oldHook, err := c.GetMutatingWebhook(hook.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldHook, hook)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for MutatingWebhook: %v", err)
	}
	return c.AdmissionregistrationV1beta1().MutatingWebhookConfigurations().Patch(hook.GetName(), types.StrategicMergePatchType, patchBytes)
}

// UpdateValidatingWebhook will update the given mutating webhook resource.
func (c *Client) UpdateValidatingWebhook(hook *admissionregistrationv1beta1.ValidatingWebhookConfiguration) (*admissionregistrationv1beta1.ValidatingWebhookConfiguration, error) {
	glog.V(4).Infof("[UPDATE ValidatingWebhook]: %s", hook.GetName())
	oldHook, err := c.GetValidatingWebhook(hook.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldHook, hook)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for ValidatingWebhook: %v", err)
	}
	return c.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations().Patch(hook.GetName(), types.StrategicMergePatchType, patchBytes)
}
