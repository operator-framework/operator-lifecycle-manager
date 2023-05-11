package olm

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const (
	// Name of packageserver API service
	PackageserverName = "v1.packages.operators.coreos.com"
)

// apiServiceResourceErrorActionable returns true if OLM can do something about any one
// of the apiService errors in errs; otherwise returns false
//
// This method can be used to determine if a CSV in a failed state due to APIService
// issues can resolve them by reinstalling
func (a *Operator) apiServiceResourceErrorActionable(err error) bool {
	filtered := utilerrors.FilterOut(err, func(e error) bool {
		_, unadoptable := e.(olmerrors.UnadoptableError)
		return !unadoptable
	})
	actionable := filtered == nil

	return actionable
}

// checkAPIServiceResources checks if all expected generated resources for the given APIService exist
func (a *Operator) checkAPIServiceResources(csv *v1alpha1.ClusterServiceVersion, hashFunc certs.PEMHash) error {
	logger := log.WithFields(log.Fields{
		"csv":       csv.GetName(),
		"namespace": csv.GetNamespace(),
	})

	errs := []error{}
	ruleChecker := install.NewCSVRuleChecker(a.lister.RbacV1().RoleLister(), a.lister.RbacV1().RoleBindingLister(), a.lister.RbacV1().ClusterRoleLister(), a.lister.RbacV1().ClusterRoleBindingLister(), csv)
	for _, desc := range csv.GetOwnedAPIServiceDescriptions() {
		apiServiceName := desc.GetName()
		logger := logger.WithFields(log.Fields{
			"apiservice": apiServiceName,
		})

		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)
		if err != nil {
			logger.Warnf("could not retrieve generated APIService")
			errs = append(errs, err)
			continue
		}

		// Check if the APIService is adoptable
		adoptable, err := install.IsAPIServiceAdoptable(a.lister, csv, apiService)
		if err != nil {
			logger.WithFields(log.Fields{"obj": "apiService", "labels": apiService.GetLabels()}).Errorf("adoption check failed - %v", err)
			errs = append(errs, err)
			return utilerrors.NewAggregate(errs)
		}

		if !adoptable {
			logger.WithFields(log.Fields{"obj": "apiService", "labels": apiService.GetLabels()}).Errorf("adoption failed")
			err := olmerrors.NewUnadoptableError("", apiServiceName)
			logger.WithError(err).Warn("found unadoptable apiservice")
			errs = append(errs, err)
			return utilerrors.NewAggregate(errs)
		}

		serviceName := install.ServiceName(desc.DeploymentName)
		service, err := a.lister.CoreV1().ServiceLister().Services(csv.GetNamespace()).Get(serviceName)
		if err != nil {
			logger.WithField("service", serviceName).Warnf("could not retrieve generated Service")
			errs = append(errs, err)
			continue
		}

		// Check if the APIService points to the correct service
		if apiService.Spec.Service.Name != serviceName || apiService.Spec.Service.Namespace != csv.GetNamespace() {
			logger.WithFields(log.Fields{"service": apiService.Spec.Service.Name, "serviceNamespace": apiService.Spec.Service.Namespace}).Warnf("APIService service reference mismatch")
			errs = append(errs, fmt.Errorf("found APIService and service reference mismatch"))
			continue
		}

		// Check if CA is Active
		caBundle := apiService.Spec.CABundle
		ca, err := certs.PEMToCert(caBundle)
		if err != nil {
			logger.Warnf("could not convert APIService CA bundle to x509 cert")
			errs = append(errs, err)
			continue
		}
		if !certs.Active(ca) {
			logger.Warnf("CA cert not active")
			errs = append(errs, fmt.Errorf("found the CA cert is not active"))
			continue
		}

		// Check if serving cert is active
		secretName := install.SecretName(serviceName)
		secret, err := a.lister.CoreV1().SecretLister().Secrets(csv.GetNamespace()).Get(secretName)
		if err != nil {
			logger.WithField("secret", secretName).Warnf("could not retrieve generated Secret: %v", err)
			errs = append(errs, err)
			continue
		}
		cert, err := certs.PEMToCert(secret.Data["tls.crt"])
		if err != nil {
			logger.Warnf("could not convert serving cert to x509 cert")
			errs = append(errs, err)
			continue
		}
		if !certs.Active(cert) {
			logger.Warnf("serving cert not active")
			errs = append(errs, fmt.Errorf("found the serving cert not active"))
			continue
		}

		// Check if CA hash matches expected
		caHash := hashFunc(caBundle)
		if hash, ok := secret.GetAnnotations()[install.OLMCAHashAnnotationKey]; !ok || hash != caHash {
			logger.WithField("secret", secretName).Warnf("secret CA cert hash does not match expected")
			errs = append(errs, fmt.Errorf("secret %s CA cert hash does not match expected", secretName))
			continue
		}

		// Check if serving cert is trusted by the CA
		hosts := []string{
			fmt.Sprintf("%s.%s", service.GetName(), csv.GetNamespace()),
			fmt.Sprintf("%s.%s.svc", service.GetName(), csv.GetNamespace()),
		}
		for _, host := range hosts {
			if err := certs.VerifyCert(ca, cert, host); err != nil {
				errs = append(errs, fmt.Errorf("could not verify cert: %s", err.Error()))
				continue
			}
		}

		// Ensure the existing Deployment has a matching CA hash annotation
		deployment, err := a.lister.AppsV1().DeploymentLister().Deployments(csv.GetNamespace()).Get(desc.DeploymentName)
		if apierrors.IsNotFound(err) || err != nil {
			logger.WithField("deployment", desc.DeploymentName).Warnf("expected Deployment could not be retrieved")
			errs = append(errs, err)
			continue
		}
		if hash, ok := deployment.Spec.Template.GetAnnotations()[install.OLMCAHashAnnotationKey]; !ok || hash != caHash {
			logger.WithField("deployment", desc.DeploymentName).Warnf("Deployment CA cert hash does not match expected")
			errs = append(errs, fmt.Errorf("deployment %s CA cert hash does not match expected", desc.DeploymentName))
			continue
		}

		// Ensure the Deployment's ServiceAccount exists
		serviceAccountName := deployment.Spec.Template.Spec.ServiceAccountName
		if serviceAccountName == "" {
			serviceAccountName = "default"
		}
		serviceAccount, err := a.lister.CoreV1().ServiceAccountLister().ServiceAccounts(deployment.GetNamespace()).Get(serviceAccountName)
		if err != nil {
			logger.WithField("serviceaccount", serviceAccountName).Warnf("could not retrieve ServiceAccount")
			errs = append(errs, err)
			continue
		}

		// Ensure RBAC permissions for the APIService are correct
		rulesMap := map[string][]rbacv1.PolicyRule{
			// Serving cert Secret Rule
			csv.GetNamespace(): {
				{
					Verbs:         []string{"get"},
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					ResourceNames: []string{secret.GetName()},
				},
			},
			install.KubeSystem:  {},
			metav1.NamespaceAll: {},
		}

		// extension-apiserver-authentication-reader
		authReaderRole, err := a.lister.RbacV1().RoleLister().Roles(install.KubeSystem).Get("extension-apiserver-authentication-reader")
		if err != nil {
			logger.Warnf("could not retrieve Role extension-apiserver-authentication-reader")
			errs = append(errs, err)
			continue
		}
		rulesMap[install.KubeSystem] = append(rulesMap[install.KubeSystem], authReaderRole.Rules...)

		// system:auth-delegator
		authDelegatorClusterRole, err := a.lister.RbacV1().ClusterRoleLister().Get("system:auth-delegator")
		if err != nil {
			logger.Warnf("could not retrieve ClusterRole system:auth-delegator")
			errs = append(errs, err)
			continue
		}
		rulesMap[metav1.NamespaceAll] = append(rulesMap[metav1.NamespaceAll], authDelegatorClusterRole.Rules...)

		for namespace, rules := range rulesMap {
			for _, rule := range rules {
				satisfied, err := ruleChecker.RuleSatisfied(serviceAccount, namespace, rule)
				if err != nil {
					logger.WithField("rule", fmt.Sprintf("%+v", rule)).Warnf("error checking Rule")
					errs = append(errs, err)
					continue
				}
				if !satisfied {
					logger.WithField("rule", fmt.Sprintf("%+v", rule)).Warnf("Rule not satisfied")
					errs = append(errs, fmt.Errorf("rule %+v not satisfied", rule))
					continue
				}
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (a *Operator) areAPIServicesAvailable(csv *v1alpha1.ClusterServiceVersion) (bool, error) {
	for _, desc := range csv.Spec.APIServiceDefinitions.Owned {
		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(desc.GetName())
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		if err != nil {
			return false, err
		}

		if !install.IsAPIServiceAvailable(apiService) {
			return false, nil
		}

		if ok, err := a.isGVKRegistered(desc.Group, desc.Version, desc.Kind); !ok || err != nil {
			return false, err
		}
	}

	return true, nil
}

// getAPIServiceCABundle returns the CA associated with an API service
func (a *Operator) getAPIServiceCABundle(csv *v1alpha1.ClusterServiceVersion, desc *v1alpha1.APIServiceDescription) ([]byte, error) {
	apiServiceName := desc.GetName()
	apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)

	if err != nil {
		return nil, fmt.Errorf("could not retrieve generated APIService: %v", err)
	}

	if len(apiService.Spec.CABundle) > 0 {
		return apiService.Spec.CABundle, nil
	}

	return nil, fmt.Errorf("unable to find CA")
}

// getWebhookCABundle returns the CA associated with a webhook
func (a *Operator) getWebhookCABundle(csv *v1alpha1.ClusterServiceVersion, desc *v1alpha1.WebhookDescription) ([]byte, error) {
	webhookLabels := ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)
	webhookLabels[install.WebhookDescKey] = desc.GenerateName
	webhookSelector := labels.SelectorFromSet(webhookLabels).String()

	switch desc.Type {
	case v1alpha1.MutatingAdmissionWebhook:
		existingWebhooks, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
		if err != nil {
			return nil, fmt.Errorf("could not retrieve generated MutatingWebhookConfiguration: %v", err)
		}

		if len(existingWebhooks.Items) > 0 {
			return existingWebhooks.Items[0].Webhooks[0].ClientConfig.CABundle, nil
		}
	case v1alpha1.ValidatingAdmissionWebhook:
		existingWebhooks, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
		if err != nil {
			return nil, fmt.Errorf("could not retrieve generated ValidatingWebhookConfiguration: %v", err)
		}

		if len(existingWebhooks.Items) > 0 {
			return existingWebhooks.Items[0].Webhooks[0].ClientConfig.CABundle, nil
		}
	case v1alpha1.ConversionWebhook:
		for _, conversionCRD := range desc.ConversionCRDs {
			// check if CRD exists on cluster
			crd, err := a.opClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), conversionCRD, metav1.GetOptions{})
			if err != nil {
				continue
			}
			if crd.Spec.Conversion == nil || crd.Spec.Conversion.Webhook == nil || crd.Spec.Conversion.Webhook.ClientConfig == nil || crd.Spec.Conversion.Webhook.ClientConfig.CABundle == nil {
				continue
			}

			return crd.Spec.Conversion.Webhook.ClientConfig.CABundle, nil
		}
	}

	return nil, fmt.Errorf("unable to find CA")
}

// updateDeploymentSpecsWithAPIServiceData transforms an install strategy to include information about apiservices
// it is used in generating hashes for deployment specs to know when something in the spec has changed,
// but duplicates a lot of installAPIServiceRequirements and should be refactored.
func (a *Operator) updateDeploymentSpecsWithAPIServiceData(csv *v1alpha1.ClusterServiceVersion, strategy install.Strategy) (install.Strategy, error) {
	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("unsupported InstallStrategy type")
	}

	// Return early if there are no owned APIServices
	if !csv.HasCAResources() {
		return strategyDetailsDeployment, nil
	}

	depSpecs := make(map[string]appsv1.DeploymentSpec)
	for _, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		depSpecs[sddSpec.Name] = sddSpec.Spec
	}

	for _, desc := range csv.Spec.APIServiceDefinitions.Owned {
		caBundle, err := a.getAPIServiceCABundle(csv, &desc)
		if err != nil {
			return nil, fmt.Errorf("could not retrieve caBundle for owned APIServices %s: %v", fmt.Sprintf("%s.%s", desc.Version, desc.Group), err)
		}
		caHash := certs.PEMSHA256(caBundle)

		depSpec, ok := depSpecs[desc.DeploymentName]
		if !ok {
			return nil, fmt.Errorf("strategyDetailsDeployment is missing deployment %s for owned APIServices %s", desc.DeploymentName, fmt.Sprintf("%s.%s", desc.Version, desc.Group))
		}

		if depSpec.Template.Spec.ServiceAccountName == "" {
			depSpec.Template.Spec.ServiceAccountName = "default"
		}

		// Update deployment with secret volume mount.
		secret, err := a.lister.CoreV1().SecretLister().Secrets(csv.GetNamespace()).Get(install.SecretName(install.ServiceName(desc.DeploymentName)))
		if err != nil {
			return nil, fmt.Errorf("unable to get secret %s", install.SecretName(install.ServiceName(desc.DeploymentName)))
		}

		install.AddDefaultCertVolumeAndVolumeMounts(&depSpec, secret.GetName())
		install.SetCAAnnotation(&depSpec, caHash)
		depSpecs[desc.DeploymentName] = depSpec
	}

	for _, desc := range csv.Spec.WebhookDefinitions {
		caBundle, err := a.getWebhookCABundle(csv, &desc)
		if err != nil {
			return nil, fmt.Errorf("could not retrieve caBundle for WebhookDescription %s: %v", desc.GenerateName, err)
		}
		caHash := certs.PEMSHA256(caBundle)

		depSpec, ok := depSpecs[desc.DeploymentName]
		if !ok {
			return nil, fmt.Errorf("strategyDetailsDeployment is missing deployment %s for WebhookDescription %s", desc.DeploymentName, desc.GenerateName)
		}

		if depSpec.Template.Spec.ServiceAccountName == "" {
			depSpec.Template.Spec.ServiceAccountName = "default"
		}

		// Update deployment with secret volume mount.
		secret, err := a.lister.CoreV1().SecretLister().Secrets(csv.GetNamespace()).Get(install.SecretName(install.ServiceName(desc.DeploymentName)))
		if err != nil {
			return nil, fmt.Errorf("unable to get secret %s", install.SecretName(install.ServiceName(desc.DeploymentName)))
		}
		install.AddDefaultCertVolumeAndVolumeMounts(&depSpec, secret.GetName())

		install.SetCAAnnotation(&depSpec, caHash)
		depSpecs[desc.DeploymentName] = depSpec
	}

	// Replace all matching DeploymentSpecs in the strategy
	for i, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		if depSpec, ok := depSpecs[sddSpec.Name]; ok {
			strategyDetailsDeployment.DeploymentSpecs[i].Spec = depSpec
		}
	}
	return strategyDetailsDeployment, nil
}

func (a *Operator) cleanUpRemovedWebhooks(csv *v1alpha1.ClusterServiceVersion) error {
	webhookLabels := ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)
	webhookSelector := labels.SelectorFromSet(webhookLabels).String()

	csvWebhookGenerateNames := make(map[string]struct{}, len(csv.Spec.WebhookDefinitions))
	for _, webhook := range csv.Spec.WebhookDefinitions {
		csvWebhookGenerateNames[webhook.GenerateName] = struct{}{}
	}

	// Delete unknown ValidatingWebhooksConfigurations owned by the CSV
	validatingWebhookConfigurationList, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
	if err != nil {
		return err
	}
	for _, webhook := range validatingWebhookConfigurationList.Items {
		webhookGenerateNameLabel, ok := webhook.GetLabels()[install.WebhookDescKey]
		if !ok {
			return fmt.Errorf("validatingWebhookConfiguration %s does not have WebhookDesc key", webhook.Name)
		}
		if _, ok := csvWebhookGenerateNames[webhookGenerateNameLabel]; !ok {
			err = a.opClient.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(context.TODO(), webhook.Name, metav1.DeleteOptions{})
			if err != nil && apierrors.IsNotFound(err) {
				return err
			}
		}
	}

	// Delete unknown MutatingWebhooksConfigurations owned by the CSV
	mutatingWebhookConfigurationList, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
	if err != nil {
		return err
	}
	for _, webhook := range mutatingWebhookConfigurationList.Items {
		webhookGenerateNameLabel, ok := webhook.GetLabels()[install.WebhookDescKey]
		if !ok {
			return fmt.Errorf("mutatingWebhookConfiguration %s does not have WebhookDesc key", webhook.Name)
		}
		if _, ok := csvWebhookGenerateNames[webhookGenerateNameLabel]; !ok {
			err = a.opClient.KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().Delete(context.TODO(), webhook.Name, metav1.DeleteOptions{})
			if err != nil && apierrors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

func (a *Operator) areWebhooksAvailable(csv *v1alpha1.ClusterServiceVersion) (bool, error) {
	err := a.cleanUpRemovedWebhooks(csv)
	if err != nil {
		return false, err
	}
	for _, desc := range csv.Spec.WebhookDefinitions {
		// Create Webhook Label Selector
		webhookLabels := ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)
		webhookLabels[install.WebhookDescKey] = desc.GenerateName
		webhookLabels[install.WebhookHashKey] = install.HashWebhookDesc(desc)
		webhookSelector := labels.SelectorFromSet(webhookLabels).String()

		webhookCount := 0
		switch desc.Type {
		case v1alpha1.ValidatingAdmissionWebhook:
			webhookList, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
			if err != nil {
				return false, err
			}
			webhookCount = len(webhookList.Items)
		case v1alpha1.MutatingAdmissionWebhook:
			webhookList, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
			if err != nil {
				return false, err
			}
			webhookCount = len(webhookList.Items)
		case v1alpha1.ConversionWebhook:
			for _, conversionCRD := range desc.ConversionCRDs {
				// check if CRD exists on cluster
				crd, err := a.opClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), conversionCRD, metav1.GetOptions{})
				if err != nil {
					log.Infof("CRD not found %v, error: %s", desc, err.Error())
					return false, err
				}

				if crd.Spec.Conversion == nil || crd.Spec.Conversion.Strategy != "Webhook" || crd.Spec.Conversion.Webhook == nil || crd.Spec.Conversion.Webhook.ClientConfig == nil || crd.Spec.Conversion.Webhook.ClientConfig.CABundle == nil {
					return false, fmt.Errorf("conversionWebhook not ready")
				}
				webhookCount++
			}
		}
		if webhookCount == 0 {
			return false, nil
		}
	}
	return true, nil
}
