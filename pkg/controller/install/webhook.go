package install

import (
	"context"
	"fmt"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"

	log "github.com/sirupsen/logrus"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func ValidWebhookRules(rules []admissionregistrationv1.RuleWithOperations) error {
	for _, rule := range rules {
		apiGroupMap := listToMap(rule.APIGroups)

		// protect OLM resources
		if contains(apiGroupMap, "*") {
			return fmt.Errorf("Webhook rules cannot include all groups")
		}

		if contains(apiGroupMap, "operators.coreos.com") {
			return fmt.Errorf("Webhook rules cannot include the OLM group")
		}

		// protect Admission Webhook resources
		if contains(apiGroupMap, "admissionregistration.k8s.io") {
			resourceGroupMap := listToMap(rule.Resources)
			if contains(resourceGroupMap, "*") || contains(resourceGroupMap, "MutatingWebhookConfiguration") || contains(resourceGroupMap, "ValidatingWebhookConfiguration") {
				return fmt.Errorf("Webhook rules cannot include MutatingWebhookConfiguration or ValidatingWebhookConfiguration resources")
			}
		}
	}
	return nil
}

func listToMap(list []string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, ele := range list {
		result[ele] = struct{}{}
	}
	return result
}

func contains(m map[string]struct{}, tar string) bool {
	_, present := m[tar]
	return present
}

func (i *StrategyDeploymentInstaller) createOrUpdateWebhook(caPEM []byte, desc v1alpha1.WebhookDescription) error {
	operatorGroups, err := i.strategyClient.GetOpLister().OperatorsV1().OperatorGroupLister().OperatorGroups(i.owner.GetNamespace()).List(labels.Everything())
	if err != nil || len(operatorGroups) != 1 {
		return fmt.Errorf("Error retrieving OperatorGroup info")
	}
	ogNamespacelabelSelector, err := operatorGroups[0].NamespaceLabelSelector()
	if err != nil {
		return err
	}

	switch desc.Type {
	case v1alpha1.ValidatingAdmissionWebhook:
		i.createOrUpdateValidatingWebhook(ogNamespacelabelSelector, caPEM, desc)
	case v1alpha1.MutatingAdmissionWebhook:
		i.createOrUpdateMutatingWebhook(ogNamespacelabelSelector, caPEM, desc)

	}
	return nil
}

func (i *StrategyDeploymentInstaller) createOrUpdateMutatingWebhook(ogNamespacelabelSelector *metav1.LabelSelector, caPEM []byte, desc v1alpha1.WebhookDescription) error {
	webhooks := []admissionregistrationv1.MutatingWebhook{
		desc.GetMutatingWebhook(i.owner.GetNamespace(), ogNamespacelabelSelector, caPEM),
	}
	existingHook, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.TODO(), desc.Name, metav1.GetOptions{})
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(i.owner, existingHook.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(existingHook, i.owner)
		}

		// Update the list of webhooks
		existingHook.Webhooks = webhooks

		// Attempt an update
		if _, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().Update(context.TODO(), existingHook, metav1.UpdateOptions{}); err != nil {
			log.Warnf("could not update MutatingWebhookConfiguration %s", existingHook.GetName())
			return err
		}
	} else if k8serrors.IsNotFound(err) {
		hook := admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: desc.Name,
				Namespace: i.owner.GetNamespace(),
			},
			Webhooks: webhooks,
		}
		// Add an owner
		ownerutil.AddNonBlockingOwner(&hook, i.owner)
		if _, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().Create(context.TODO(), &hook, metav1.CreateOptions{}); err != nil {
			log.Errorf("Webhooks: Error creating mutating MutatingVebhookConfiguration: %v", err)
			return err
		}
	} else {
		return err
	}

	return nil
}

func (i *StrategyDeploymentInstaller) createOrUpdateValidatingWebhook(ogNamespacelabelSelector *metav1.LabelSelector, caPEM []byte, desc v1alpha1.WebhookDescription) error {
	webhooks := []admissionregistrationv1.ValidatingWebhook{
		desc.GetValidatingWebhook(i.owner.GetNamespace(), ogNamespacelabelSelector, caPEM),
	}
	existingHook, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.TODO(), desc.Name, metav1.GetOptions{})
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(i.owner, existingHook.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(existingHook, i.owner)
		}

		// Update the list of webhooks
		existingHook.Webhooks = webhooks

		// Attempt an update
		if _, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(context.TODO(), existingHook, metav1.UpdateOptions{}); err != nil {
			log.Warnf("could not update ValidatingWebhookConfiguration %s", existingHook.GetName())
			return err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create a ValidatingWebhookConfiguration
		hook := admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: desc.Name,
				Namespace: i.owner.GetNamespace(),
			},
			Webhooks: webhooks,
		}

		// Add an owner
		ownerutil.AddNonBlockingOwner(&hook, i.owner)
		if _, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(context.TODO(), &hook, metav1.CreateOptions{}); err != nil {
			log.Errorf("Webhooks: Error create creating ValidationVebhookConfiguration: %v", err)
			return err
		}
	} else {
		return err
	}
	return nil
}
