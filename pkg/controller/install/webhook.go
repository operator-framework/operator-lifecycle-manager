package install

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	hashutil "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/hash"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"

	log "github.com/sirupsen/logrus"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/rand"
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
	case v1alpha1.ConversionWebhook:
		i.createOrUpdateConversionWebhook(caPEM, desc)
	}
	return nil
}

func (i *StrategyDeploymentInstaller) createOrUpdateMutatingWebhook(ogNamespacelabelSelector *metav1.LabelSelector, caPEM []byte, desc v1alpha1.WebhookDescription) error {
	webhookLabels := ownerutil.OwnerLabel(i.owner, i.owner.GetObjectKind().GroupVersionKind().Kind)
	webhookLabels[WebhookDescKey] = desc.GenerateName
	webhookSelector := labels.SelectorFromSet(webhookLabels).String()

	existingWebhooks, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
	if err != nil {
		return err
	}

	if len(existingWebhooks.Items) == 0 {
		// Create a MutatingWebhookConfiguration
		webhook := admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: desc.GenerateName + "-",
				Namespace:    i.owner.GetNamespace(),
				Labels:       ownerutil.OwnerLabel(i.owner, i.owner.GetObjectKind().GroupVersionKind().Kind),
			},
			Webhooks: []admissionregistrationv1.MutatingWebhook{
				desc.GetMutatingWebhook(i.owner.GetNamespace(), ogNamespacelabelSelector, caPEM),
			},
		}
		addWebhookLabels(&webhook, desc)

		if _, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().Create(context.TODO(), &webhook, metav1.CreateOptions{}); err != nil {
			log.Errorf("Webhooks: Error creating MutatingWebhookConfiguration: %v", err)
			return err
		}
	}
	for _, webhook := range existingWebhooks.Items {
		// Update the list of webhooks
		webhook.Webhooks = []admissionregistrationv1.MutatingWebhook{
			desc.GetMutatingWebhook(i.owner.GetNamespace(), ogNamespacelabelSelector, caPEM),
		}
		addWebhookLabels(&webhook, desc)

		// Attempt an update
		if _, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().Update(context.TODO(), &webhook, metav1.UpdateOptions{}); err != nil {
			log.Warnf("could not update MutatingWebhookConfiguration %s", webhook.GetName())
			return err
		}
	}

	return nil
}

func (i *StrategyDeploymentInstaller) createOrUpdateValidatingWebhook(ogNamespacelabelSelector *metav1.LabelSelector, caPEM []byte, desc v1alpha1.WebhookDescription) error {
	webhookLabels := ownerutil.OwnerLabel(i.owner, i.owner.GetObjectKind().GroupVersionKind().Kind)
	webhookLabels[WebhookDescKey] = desc.GenerateName
	webhookSelector := labels.SelectorFromSet(webhookLabels).String()

	existingWebhooks, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
	if err != nil {
		return err
	}

	if len(existingWebhooks.Items) == 0 {
		// Create a ValidatingWebhookConfiguration
		webhook := admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: desc.GenerateName + "-",
				Namespace:    i.owner.GetNamespace(),
				Labels:       ownerutil.OwnerLabel(i.owner, i.owner.GetObjectKind().GroupVersionKind().Kind),
			},
			Webhooks: []admissionregistrationv1.ValidatingWebhook{
				desc.GetValidatingWebhook(i.owner.GetNamespace(), ogNamespacelabelSelector, caPEM),
			},
		}
		addWebhookLabels(&webhook, desc)

		if _, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(context.TODO(), &webhook, metav1.CreateOptions{}); err != nil {
			log.Errorf("Webhooks: Error creating ValidatingWebhookConfiguration: %v", err)
			return err
		}

		return nil
	}
	for _, webhook := range existingWebhooks.Items {
		// Update the list of webhooks
		webhook.Webhooks = []admissionregistrationv1.ValidatingWebhook{
			desc.GetValidatingWebhook(i.owner.GetNamespace(), ogNamespacelabelSelector, caPEM),
		}
		addWebhookLabels(&webhook, desc)

		// Attempt an update
		if _, err := i.strategyClient.GetOpClient().KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(context.TODO(), &webhook, metav1.UpdateOptions{}); err != nil {
			log.Warnf("could not update ValidatingWebhookConfiguration %s", webhook.GetName())
			return err
		}
	}

	return nil
}

// check if csv supports only AllNamespaces install mode
func isSingletonOperator(csv v1alpha1.ClusterServiceVersion) bool {
	// check if AllNamespaces is supported and other install modes are not supported
	for _, installMode := range csv.Spec.InstallModes {
		if installMode.Type == v1alpha1.InstallModeTypeAllNamespaces && !installMode.Supported {
			return false
		}
		if installMode.Type != v1alpha1.InstallModeTypeAllNamespaces && installMode.Supported {
			return false
		}
	}
	return true
}

func (i *StrategyDeploymentInstaller) createOrUpdateConversionWebhook(caPEM []byte, desc v1alpha1.WebhookDescription) error {
	// get a list of owned CRDs
	csv, ok := i.owner.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		return fmt.Errorf("ConversionWebhook owner must be a ClusterServiceVersion")
	}

	if !isSingletonOperator(*csv) {
		return fmt.Errorf("CSVs with conversion webhooks must support only AllNamespaces")
	}

	if len(desc.ConversionCRDs) == 0 {
		return fmt.Errorf("Conversion Webhook must have at least one CRD specified")
	}

	// iterate over all the ConversionCRDs
	for _, conversionCRD := range desc.ConversionCRDs {
		// Get existing CRD on cluster
		crd, err := i.strategyClient.GetOpClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), conversionCRD, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("Unable to get CRD %s specified in Conversion Webhook: %v", conversionCRD, err)
		}

		// check if this CRD is an owned CRD
		foundCRD := false
		for _, ownedCRD := range csv.Spec.CustomResourceDefinitions.Owned {
			if ownedCRD.Name == conversionCRD {
				foundCRD = true
				break
			}
		}
		if !foundCRD {
			return fmt.Errorf("CSV %s does not own CRD %s", csv.GetName(), conversionCRD)
		}

		// crd.Spec.Conversion.Strategy specifies how custom resources are converted between versions.
		// Allowed values are:
		// 	- None: The converter only change the apiVersion and would not touch any other field in the custom resource.
		// 	- Webhook: API Server will call to an external webhook to do the conversion. This requires crd.Spec.preserveUnknownFields to be false.
		// References:
		//  - https://docs.openshift.com/container-platform/4.5/rest_api/extension_apis/customresourcedefinition-apiextensions-k8s-io-v1.html
		// 	- https://kubernetes.io/blog/2019/06/20/crd-structural-schema/#pruning-don-t-preserve-unknown-fields
		// By default the strategy is none
		// Reference:
		// 	- https://v1-15.docs.kubernetes.io/docs/tasks/access-kubernetes-api/custom-resources/custom-resource-definition-versioning/#specify-multiple-versions
		if crd.Spec.PreserveUnknownFields != false {
			return fmt.Errorf("crd.Spec.PreserveUnknownFields must be false to let API Server call webhook to do the conversion")
		}

		// Conversion WebhookClientConfig should not be set when Strategy is None
		// https://v1-15.docs.kubernetes.io/docs/tasks/access-kubernetes-api/custom-resources/custom-resource-definition-versioning/#specify-multiple-versions
		// Conversion WebhookClientConfig needs to be set when Strategy is None
		// https://v1-15.docs.kubernetes.io/docs/tasks/access-kubernetes-api/custom-resources/custom-resource-definition-versioning/#configure-customresourcedefinition-to-use-conversion-webhooks

		// use user defined path for CRD conversion webhook, else set default value
		conversionWebhookPath := "/"
		if desc.WebhookPath != nil {
			conversionWebhookPath = *desc.WebhookPath
		}

		// Override Name, Namespace, and CABundle
		crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
			Strategy: "Webhook",
			Webhook: &apiextensionsv1.WebhookConversion{
				ClientConfig: &apiextensionsv1.WebhookClientConfig{
					Service: &apiextensionsv1.ServiceReference{
						Namespace: i.owner.GetNamespace(),
						Name:      desc.DomainName() + "-service",
						Path:      &conversionWebhookPath,
						Port:      &desc.ContainerPort,
					},
					CABundle: caPEM,
				},
				ConversionReviewVersions: desc.AdmissionReviewVersions,
			},
		}

		// update CRD conversion Specs
		if _, err = i.strategyClient.GetOpClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Update(context.TODO(), crd, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("Error updating CRD with Conversion info: %v", err)
		}
	}

	return nil
}

const WebhookDescKey = "olm.webhook-description-generate-name"
const WebhookHashKey = "olm.webhook-description-hash"

// addWebhookLabels adds webhook labels to an object
func addWebhookLabels(object metav1.Object, webhookDesc v1alpha1.WebhookDescription) error {
	labels := object.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[WebhookDescKey] = webhookDesc.GenerateName
	labels[WebhookHashKey] = HashWebhookDesc(webhookDesc)
	object.SetLabels(labels)

	return nil
}

// HashWebhookDesc calculates a hash given a webhookDescription
func HashWebhookDesc(webhookDesc v1alpha1.WebhookDescription) string {
	hasher := fnv.New32a()
	hashutil.DeepHashObject(hasher, &webhookDesc)
	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
}
