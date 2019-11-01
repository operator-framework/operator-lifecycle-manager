// TODO: Refactor this code with the API Cert code.
package olm

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

func (a *Operator) installWebhookRequirements(csv *v1alpha1.ClusterServiceVersion, strategy install.Strategy) (install.Strategy, error) {
	logger := log.WithFields(log.Fields{
		"csv":       csv.GetName(),
		"namespace": csv.GetNamespace(),
	})

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*install.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("unsupported InstallStrategy type")
	}

	// Return early if there are no WebhookDefinitions
	webhookDescriptions := csv.Spec.WebhookDefinitions
	if len(webhookDescriptions) == 0 {
		return strategyDetailsDeployment, nil
	}

	// Create the CA
	expiration := time.Now().Add(DefaultCertValidFor)
	ca, err := certs.GenerateCA(expiration, Organization)
	if err != nil {
		logger.Debug("failed to generate CA")
		return nil, err
	}
	rotateAt := expiration.Add(-1 * DefaultCertMinFresh)

	depSpecs := make(map[string]appsv1.DeploymentSpec)
	for _, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		depSpecs[sddSpec.Name] = sddSpec.Spec
	}

	// Create all resources required, and update the matching DeploymentSpec's Volume and VolumeMounts
	// Get List of Webhooks
	for _, desc := range webhookDescriptions {
		depSpec, ok := depSpecs[desc.DeploymentName]
		if !ok {
			return nil, fmt.Errorf("StrategyDetailsDeployment missing deployment %s for webhook", desc.DeploymentName)
		}

		newDepSpec, err := a.installWebhook(desc, ca, rotateAt, depSpec, csv)
		if err != nil {
			return nil, err
		}
		depSpecs[desc.DeploymentName] = *newDepSpec
	}

	// Replace all matching DeploymentSpecs in the strategy
	for i, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		if depSpec, ok := depSpecs[sddSpec.Name]; ok {
			strategyDetailsDeployment.DeploymentSpecs[i].Spec = depSpec
		}
	}

	// Set CSV cert status
	csv.Status.CertsLastUpdated = metav1.Now()
	csv.Status.CertsRotateAt = metav1.NewTime(rotateAt)

	return strategyDetailsDeployment, nil
}

func (a *Operator) installWebhook(desc v1alpha1.WebhookDescription, ca *certs.KeyPair, rotateAt time.Time, depSpec appsv1.DeploymentSpec, csv *v1alpha1.ClusterServiceVersion) (*appsv1.DeploymentSpec, error) {
	logger := log.WithFields(log.Fields{
		"csv":       csv.GetName(),
		"namespace": csv.GetNamespace(),
		"webhook":   desc.Name,
	})

	// Create a service for the deployment
	containerPort := 443
	if desc.ContainerPort > 0 {
		containerPort = int(desc.ContainerPort)
	}
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       int32(443),
					TargetPort: intstr.FromInt(containerPort),
				},
			},
			Selector: depSpec.Selector.MatchLabels,
		},
	}
	service.SetName(desc.MorphDomainName() + "-svc")
	service.SetNamespace(csv.GetNamespace())
	ownerutil.AddNonBlockingOwner(service, csv)

	existingService, err := a.lister.CoreV1().ServiceLister().Services(csv.GetNamespace()).Get(service.GetName())
	if err == nil {
		if !ownerutil.Adoptable(csv, existingService.GetOwnerReferences()) {
			return nil, fmt.Errorf("service %s not safe to replace: extraneous ownerreferences found", service.GetName())
		}
		service.SetOwnerReferences(append(service.GetOwnerReferences(), existingService.GetOwnerReferences()...))

		// Delete the Service to replace
		deleteErr := a.opClient.DeleteService(service.GetNamespace(), service.GetName(), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(deleteErr) {
			return nil, fmt.Errorf("could not delete existing service %s", service.GetName())
		}
	}

	// Attempt to create the Service
	_, err = a.opClient.CreateService(service)
	if err != nil {
		logger.Warnf("could not create service %s", service.GetName())
		return nil, fmt.Errorf("could not create service %s: %s", service.GetName(), err.Error())
	}

	// Create signed serving cert
	hosts := []string{
		fmt.Sprintf("%s.%s", service.GetName(), csv.GetNamespace()),
		fmt.Sprintf("%s.%s.svc", service.GetName(), csv.GetNamespace()),
	}
	servingPair, err := certs.CreateSignedServingPair(rotateAt, Organization, ca, hosts)
	if err != nil {
		logger.Warnf("could not generate signed certs for hosts %v", hosts)
		return nil, err
	}

	// Create Secret for serving cert
	certPEM, privPEM, err := servingPair.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert serving certificate and private key to PEM format for Webhook %s", desc.Name)
		return nil, err
	}

	secret := &corev1.Secret{
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": privPEM,
		},
		Type: corev1.SecretTypeTLS,
	}
	secret.SetName(desc.MorphDomainName() + "-cert")
	secret.SetNamespace(csv.GetNamespace())

	// Add olmcasha hash as a label to the
	caPEM, _, err := ca.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert CA certificate to PEM format for Webhook %s", desc.Name)
		return nil, err
	}
	caHash := certs.PEMSHA256(caPEM)
	secret.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	existingSecret, err := a.lister.CoreV1().SecretLister().Secrets(csv.GetNamespace()).Get(secret.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingSecret.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secret, csv)
		}

		// Attempt an update
		if _, err := a.opClient.UpdateSecret(secret); err != nil {
			logger.Warnf("could not update secret %s", secret.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the secret
		ownerutil.AddNonBlockingOwner(secret, csv)
		_, err = a.opClient.CreateSecret(secret)
		if err != nil {
			log.Warnf("could not create secret %s", secret.GetName())
			return nil, err
		}
	} else {
		return nil, err
	}

	// create Role and RoleBinding to allow the deployment to mount the Secret
	secretRole := &rbacv1.Role{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"get"},
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{secret.GetName()},
			},
		},
	}
	secretRole.SetName(secret.GetName())
	secretRole.SetNamespace(csv.GetNamespace())

	existingSecretRole, err := a.lister.RbacV1().RoleLister().Roles(csv.GetNamespace()).Get(secretRole.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingSecretRole.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secretRole, csv)
		}

		// Attempt an update
		if _, err := a.opClient.UpdateRole(secretRole); err != nil {
			logger.Warnf("could not update secret role %s", secretRole.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRole, csv)
		_, err = a.opClient.CreateRole(secretRole)
		if err != nil {
			log.Warnf("could not create secret role %s", secretRole.GetName())
			return nil, err
		}
	} else {
		return nil, err
	}

	if depSpec.Template.Spec.ServiceAccountName == "" {
		depSpec.Template.Spec.ServiceAccountName = "default"
	}

	secretRoleBinding := &rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      depSpec.Template.Spec.ServiceAccountName,
				Namespace: csv.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     secretRole.GetName(),
		},
	}
	secretRoleBinding.SetName(secret.GetName())
	secretRoleBinding.SetNamespace(csv.GetNamespace())

	existingSecretRoleBinding, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(csv.GetNamespace()).Get(secretRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingSecretRoleBinding.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secretRoleBinding, csv)
		}

		// Attempt an update
		if _, err := a.opClient.UpdateRoleBinding(secretRoleBinding); err != nil {
			logger.Warnf("could not update secret rolebinding %s", secretRoleBinding.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRoleBinding, csv)
		_, err = a.opClient.CreateRoleBinding(secretRoleBinding)
		if err != nil {
			log.Warnf("could not create secret rolebinding with dep spec: %+v", depSpec)
			return nil, err
		}
	} else {
		return nil, err
	}

	// Update deployment with secret volume mount.
	volume := corev1.Volume{
		Name: "webhook-cert",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secret.GetName(),
				Items: []corev1.KeyToPath{
					{
						Key:  "tls.crt",
						Path: "webhook.crt",
					},
					{
						Key:  "tls.key",
						Path: "webhook.key",
					},
				},
			},
		},
	}

	replaced := false
	for i, v := range depSpec.Template.Spec.Volumes {
		if v.Name == volume.Name {
			depSpec.Template.Spec.Volumes[i] = volume
			replaced = true
			break
		}
	}
	if !replaced {
		depSpec.Template.Spec.Volumes = append(depSpec.Template.Spec.Volumes, volume)
	}

	mount := corev1.VolumeMount{
		Name:      volume.Name,
		MountPath: "/webhook.local.config/certificates",
	}
	for i, container := range depSpec.Template.Spec.Containers {
		found := false
		for j, m := range container.VolumeMounts {
			if m.Name == mount.Name {
				found = true
				break
			}

			// Replace if mounting to the same location.
			if m.MountPath == mount.MountPath {
				container.VolumeMounts[j] = mount
				found = true
				break
			}
		}
		if !found {
			container.VolumeMounts = append(container.VolumeMounts, mount)
		}

		depSpec.Template.Spec.Containers[i] = container
	}

	// Setting the olm hash label forces a rollout and ensures that the new secret
	// is used by the webhook if not hot reloading.
	depSpec.Template.ObjectMeta.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	switch desc.Type {
	case v1alpha1.InstallWebhookValidating:
		webhooks := []admissionregistrationv1.ValidatingWebhook{
			desc.GetValidatingWebhook(csv.GetNamespace(), caPEM),
		}
		hook := admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: desc.Name,
				Namespace: csv.GetNamespace(),
			},
			Webhooks: webhooks,
		}

		ownerutil.AddNonBlockingOwner(&hook, csv)
		log.Infof("Webhooks: Creating ValidatingWebhookConfiguration %v", hook)
		if _, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(&hook); err != nil {
			log.Errorf("Webhooks: Error create/updating validation webhook: %v", err)
			return nil, err
		}
	case v1alpha1.InstallWebhookMutating:
		webhooks := []admissionregistrationv1.MutatingWebhook{
			desc.GetMutatingWebhook(csv.GetNamespace(), caPEM),
		}
		hook := admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: desc.Name,
				Namespace: csv.GetNamespace(),
			},
			Webhooks: webhooks,
		}

		ownerutil.AddNonBlockingOwner(&hook, csv)
		log.Infof("Webhooks: Creating MutatingWebhookConfiguration %v", hook)
		if _, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().Create(&hook); err != nil {
			log.Errorf("Webhooks: Error create/updating Mutating webhook: %v", err)
			return nil, err
		}
	}

	if err != nil {
		logger.Warnf("could not create or update Webhook")
		return nil, err
	}

	return &depSpec, nil
}
