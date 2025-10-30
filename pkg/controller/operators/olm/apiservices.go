package olm

import (
	"context"
	"fmt"

	hashutil "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/hash"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
		_, err = certs.PEMToCert(caBundle)
		if err != nil {
			logger.Warnf("could not convert APIService CA bundle to x509 cert")
			errs = append(errs, err)
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
		_, err = certs.PEMToCert(secret.Data["tls.crt"])
		if err != nil {
			logger.Warnf("could not convert serving cert to x509 cert")
			errs = append(errs, err)
			continue
		}

		// Check if CA hash matches expected
		caHash := hashFunc(caBundle)
		if hash, ok := secret.GetAnnotations()[install.OLMCAHashAnnotationKey]; !ok || hash != caHash {
			logger.WithField("secret", secretName).Warnf("secret CA cert hash does not match expected")
			errs = append(errs, fmt.Errorf("secret %s CA cert hash does not match expected", secretName))
			continue
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
		_, err = a.opClient.KubernetesInterface().CoreV1().ServiceAccounts(deployment.GetNamespace()).Get(context.TODO(), serviceAccountName, metav1.GetOptions{})
		if err != nil {
			logger.WithError(err).WithField("serviceaccount", serviceAccountName).Warnf("could not retrieve ServiceAccount")
			errs = append(errs, err)
		}

		if _, err := a.lister.RbacV1().RoleLister().Roles(secret.GetNamespace()).Get(secret.GetName()); err != nil {
			logger.WithError(err).Warnf("could not retrieve role %s/%s", secret.GetNamespace(), secret.GetName())
			errs = append(errs, err)
		}
		if _, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(secret.GetNamespace()).Get(secret.GetName()); err != nil {
			logger.WithError(err).Warnf("could not retrieve role binding %s/%s", secret.GetNamespace(), secret.GetName())
			errs = append(errs, err)
		}
		if _, err := a.lister.RbacV1().ClusterRoleBindingLister().Get(install.AuthDelegatorClusterRoleBindingName(service.GetName())); err != nil {
			logger.WithError(err).Warnf("could not retrieve auth delegator cluster role binding %s", install.AuthDelegatorClusterRoleBindingName(service.GetName()))
			errs = append(errs, err)
		}
		if _, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(install.KubeSystem).Get(install.AuthReaderRoleBindingName(service.GetName())); err != nil {
			logger.WithError(err).Warnf("could not retrieve role binding %s/%s", install.KubeSystem, install.AuthReaderRoleBindingName(service.GetName()))
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

// isAPIServiceBackendDisrupted checks if the APIService is unavailable due to expected pod disruption
// (e.g., during node reboot or cluster upgrade) rather than an actual failure.
// According to the Progressing condition contract, operators should not report Progressing=True
// only because pods are adjusting to new nodes or rebooting during cluster upgrade.
func (a *Operator) isAPIServiceBackendDisrupted(csv *v1alpha1.ClusterServiceVersion, apiServiceName string) bool {
	// Get the deployment that backs this APIService
	// For most APIServices, the deployment name matches the CSV name or is specified in the CSV

	// Try to find the deployment from the CSV's install strategy
	strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		a.logger.Debugf("Unable to unmarshal strategy for CSV %s: %v", csv.Name, err)
		return false
	}

	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		a.logger.Debugf("CSV %s does not use deployment strategy", csv.Name)
		return false
	}

	// Check each deployment's pods
	for _, deploymentSpec := range strategyDetailsDeployment.DeploymentSpecs {
		deployment, err := a.lister.AppsV1().DeploymentLister().Deployments(csv.Namespace).Get(deploymentSpec.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			a.logger.Debugf("Error getting deployment %s: %v", deploymentSpec.Name, err)
			continue
		}

		// Check if deployment is being updated or rolling out
		if deployment.Status.UnavailableReplicas > 0 ||
			deployment.Status.UpdatedReplicas < deployment.Status.Replicas {
			a.logger.Debugf("Deployment %s has unavailable replicas, likely due to pod disruption", deploymentSpec.Name)

			// Check pod status to confirm disruption
			selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
			if err != nil {
				a.logger.Debugf("Error parsing deployment selector: %v", err)
				continue
			}

			pods, err := a.lister.CoreV1().PodLister().Pods(csv.Namespace).List(selector)
			if err != nil {
				a.logger.Debugf("Error listing pods: %v", err)
				continue
			}

			// Check if any pod is in Terminating or ContainerCreating state
			for _, pod := range pods {
				// Pod is terminating (DeletionTimestamp is set)
				if pod.DeletionTimestamp != nil {
					a.logger.Debugf("Pod %s is terminating - expected disruption", pod.Name)
					return true
				}

				// Pod is pending (being scheduled/created)
				if pod.Status.Phase == corev1.PodPending {
					a.logger.Debugf("Pod %s is pending - expected disruption", pod.Name)
					return true
				}

				// Check container statuses for restarting containers
				for _, containerStatus := range pod.Status.ContainerStatuses {
					if containerStatus.State.Waiting != nil {
						reason := containerStatus.State.Waiting.Reason
						if reason == "ContainerCreating" || reason == "PodInitializing" {
							a.logger.Debugf("Pod %s container is starting - expected disruption", pod.Name)
							return true
						}
					}
				}
			}
		}
	}

	return false
}

func (a *Operator) areAPIServicesAvailable(csv *v1alpha1.ClusterServiceVersion) (bool, error) {
	for _, desc := range csv.Spec.APIServiceDefinitions.Owned {
		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(desc.GetName())
		if apierrors.IsNotFound(err) {
			a.logger.Debugf("APIRegistration APIService %s not found", desc.GetName())
			return false, nil
		}

		if err != nil {
			return false, err
		}

		if !install.IsAPIServiceAvailable(apiService) {
			a.logger.Debugf("APIService not available for %s", desc.GetName())

			// Check if this unavailability is due to expected pod disruption
			// If so, we should not immediately mark as failed or trigger Progressing=True
			if a.isAPIServiceBackendDisrupted(csv, desc.GetName()) {
				a.logger.Infof("APIService %s unavailable due to pod disruption (e.g., node reboot), will retry", desc.GetName())
				// Return an error to trigger retry, but don't mark as definitively unavailable
				return false, olmerrors.NewRetryableError(fmt.Errorf("APIService %s temporarily unavailable due to pod disruption", desc.GetName()))
			}

			return false, nil
		}

		if ok, err := a.isGVKRegistered(desc.Group, desc.Version, desc.Kind); !ok || err != nil {
			a.logger.Debugf("%s.%s/%s not registered for %s", desc.Group, desc.Version, desc.Kind, desc.GetName())
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
		hash, err := hashutil.DeepHashObject(&desc)
		if err != nil {
			return false, err
		}
		webhookLabels[install.WebhookHashKey] = hash
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
