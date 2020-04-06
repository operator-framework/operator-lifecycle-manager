package install

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const (
	// DefaultCertMinFresh is the default min-fresh value - 1 day
	DefaultCertMinFresh = time.Hour * 24
	// DefaultCertValidFor is the default duration a cert can be valid for - 2 years
	DefaultCertValidFor = time.Hour * 24 * 730
	// OLMCAHashAnnotationKey is the label key used to store the hash of the CA cert
	OLMCAHashAnnotationKey = "olmcahash"
	// Organization is the organization name used in the generation of x509 certs
	Organization = "Red Hat, Inc."
	// Name of packageserver API service
	PackageserverName = "v1.packages.operators.coreos.com"
	// Kubernetes System namespace
	kubeSystem = "kube-system"
)

func secretName(serviceName string) string {
	return serviceName + "-cert"
}

func serviceName(deploymentName string) string {
	return deploymentName + "-service"
}

func (i *StrategyDeploymentInstaller) apiServiceDescriptionsForDeployment(deploymentName string) []installerAPIServiceDescriptions {
	result := []installerAPIServiceDescriptions{}
	for _, desc := range i.apiServiceDescriptions {
		if desc.apiServiceDescription.DeploymentName == deploymentName {
			result = append(result, desc)
		}
	}
	return result
}

func (i *StrategyDeploymentInstaller) installOwnedAPIServiceRequirements(strategy Strategy) (*v1alpha1.StrategyDetailsDeployment, error) {
	logger := log.WithFields(log.Fields{})

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("unsupported InstallStrategy type")
	}

	// Create the CA
	expiration := time.Now().Add(DefaultCertValidFor)
	ca, err := certs.GenerateCA(expiration, Organization)
	if err != nil {
		logger.Debug("failed to generate CA")
		return nil, err
	}
	rotateAt := expiration.Add(-1 * DefaultCertMinFresh)

	for n, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		descs := i.apiServiceDescriptionsForDeployment(sddSpec.Name)
		if len(descs) == 0 {
			continue
		}

		// Update the deployment for each api service desc
		newDepSpec, err := i.installAPIServiceRequirements(sddSpec.Name, ca, rotateAt, sddSpec.Spec, getServicePorts(descs))
		if err != nil {
			return nil, err
		}

		caPEM, _, err := ca.ToPEM()
		if err != nil {
			logger.Warnf("unable to convert CA certificate to PEM format for Deployment %s", sddSpec.Name)
			return nil, err
		}

		for _, desc := range descs {
			i.updateCa(desc.apiServiceDescription.Name, caPEM)
			// Cleanup legacy resources
			err = i.deleteLegacyAPIServiceResources(desc)
			if err != nil {
				return nil, err
			}
		}

		strategyDetailsDeployment.DeploymentSpecs[n].Spec = *newDepSpec
	}
	return strategyDetailsDeployment, nil
}

func (i *StrategyDeploymentInstaller) updateCa(descName string, caPEM []byte) {
	for n := range i.apiServiceDescriptions {
		if i.apiServiceDescriptions[n].apiServiceDescription.Name == descName {
			i.apiServiceDescriptions[n].caPEM = caPEM
		}
	}
}

func getServicePorts(descs []installerAPIServiceDescriptions) []corev1.ServicePort {
	result := []corev1.ServicePort{}
	for _, desc := range descs {
		if !containsServicePort(result, getServicePort(desc)) {
			result = append(result, getServicePort(desc))
		}
	}

	return result
}

func getServicePort(desc installerAPIServiceDescriptions) corev1.ServicePort {
	containerPort := 443
	if desc.apiServiceDescription.ContainerPort > 0 {
		containerPort = int(desc.apiServiceDescription.ContainerPort)
	}
	return corev1.ServicePort{
		Name:       strconv.Itoa(containerPort),
		Port:       int32(containerPort),
		TargetPort: intstr.FromInt(containerPort),
	}
}

func containsServicePort(servicePorts []corev1.ServicePort, targetServicePort corev1.ServicePort) bool {
	for _, servicePort := range servicePorts {
		if servicePort == targetServicePort {
			return true
		}
	}

	return false
}

func (i *StrategyDeploymentInstaller) installAPIServiceRequirements(deploymentName string, ca *certs.KeyPair, rotateAt time.Time, depSpec appsv1.DeploymentSpec, ports []corev1.ServicePort) (*appsv1.DeploymentSpec, error) {
	logger := log.WithFields(log.Fields{})

	// Create a service for the deployment
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports:    ports,
			Selector: depSpec.Selector.MatchLabels,
		},
	}
	service.SetName(serviceName(deploymentName))
	service.SetNamespace(i.owner.GetNamespace())
	ownerutil.AddNonBlockingOwner(service, i.owner)

	existingService, err := i.strategyClient.GetOpLister().CoreV1().ServiceLister().Services(i.owner.GetNamespace()).Get(service.GetName())
	if err == nil {
		if !ownerutil.Adoptable(i.owner, existingService.GetOwnerReferences()) {
			return nil, fmt.Errorf("service %s not safe to replace: extraneous ownerreferences found", service.GetName())
		}
		service.SetOwnerReferences(append(service.GetOwnerReferences(), existingService.GetOwnerReferences()...))

		// Delete the Service to replace
		deleteErr := i.strategyClient.GetOpClient().DeleteService(service.GetNamespace(), service.GetName(), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(deleteErr) {
			return nil, fmt.Errorf("could not delete existing service %s", service.GetName())
		}
	}

	// Attempt to create the Service
	_, err = i.strategyClient.GetOpClient().CreateService(service)
	if err != nil {
		logger.Warnf("could not create service %s", service.GetName())
		return nil, fmt.Errorf("could not create service %s: %s", service.GetName(), err.Error())
	}

	// Create signed serving cert
	hosts := []string{
		fmt.Sprintf("%s.%s", service.GetName(), i.owner.GetNamespace()),
		fmt.Sprintf("%s.%s.svc", service.GetName(), i.owner.GetNamespace()),
	}
	servingPair, err := certs.CreateSignedServingPair(rotateAt, Organization, ca, hosts)
	if err != nil {
		logger.Warnf("could not generate signed certs for hosts %v", hosts)
		return nil, err
	}

	// Create Secret for serving cert
	certPEM, privPEM, err := servingPair.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert serving certificate and private key to PEM format for Service %s", service.GetName())
		return nil, err
	}

	secret := &corev1.Secret{
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": privPEM,
		},
		Type: corev1.SecretTypeTLS,
	}
	secret.SetName(secretName(service.GetName()))
	secret.SetNamespace(i.owner.GetNamespace())

	// Add olmcahash as a label to the caPEM
	caPEM, _, err := ca.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert CA certificate to PEM format for Service %s", service)
		return nil, err
	}
	caHash := certs.PEMSHA256(caPEM)
	secret.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	existingSecret, err := i.strategyClient.GetOpLister().CoreV1().SecretLister().Secrets(i.owner.GetNamespace()).Get(secret.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(i.owner, existingSecret.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secret, i.owner)
		}

		// Attempt an update
		if _, err := i.strategyClient.GetOpClient().UpdateSecret(secret); err != nil {
			logger.Warnf("could not update secret %s", secret.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the secret
		ownerutil.AddNonBlockingOwner(secret, i.owner)
		_, err = i.strategyClient.GetOpClient().CreateSecret(secret)
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
	secretRole.SetNamespace(i.owner.GetNamespace())

	existingSecretRole, err := i.strategyClient.GetOpLister().RbacV1().RoleLister().Roles(i.owner.GetNamespace()).Get(secretRole.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(i.owner, existingSecretRole.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secretRole, i.owner)
		}

		// Attempt an update
		if _, err := i.strategyClient.GetOpClient().UpdateRole(secretRole); err != nil {
			logger.Warnf("could not update secret role %s", secretRole.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRole, i.owner)
		_, err = i.strategyClient.GetOpClient().CreateRole(secretRole)
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
				Namespace: i.owner.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     secretRole.GetName(),
		},
	}
	secretRoleBinding.SetName(secret.GetName())
	secretRoleBinding.SetNamespace(i.owner.GetNamespace())

	existingSecretRoleBinding, err := i.strategyClient.GetOpLister().RbacV1().RoleBindingLister().RoleBindings(i.owner.GetNamespace()).Get(secretRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(i.owner, existingSecretRoleBinding.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secretRoleBinding, i.owner)
		}

		// Attempt an update
		if _, err := i.strategyClient.GetOpClient().UpdateRoleBinding(secretRoleBinding); err != nil {
			logger.Warnf("could not update secret rolebinding %s", secretRoleBinding.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRoleBinding, i.owner)
		_, err = i.strategyClient.GetOpClient().CreateRoleBinding(secretRoleBinding)
		if err != nil {
			log.Warnf("could not create secret rolebinding with dep spec: %#v", depSpec)
			return nil, err
		}
	} else {
		return nil, err
	}

	// create ClusterRoleBinding to system:auth-delegator Role
	authDelegatorClusterRoleBinding := &rbacv1.ClusterRoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      depSpec.Template.Spec.ServiceAccountName,
				Namespace: i.owner.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:auth-delegator",
		},
	}
	authDelegatorClusterRoleBinding.SetName(service.GetName() + "-system:auth-delegator")

	existingAuthDelegatorClusterRoleBinding, err := i.strategyClient.GetOpLister().RbacV1().ClusterRoleBindingLister().Get(authDelegatorClusterRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain.
		if ownerutil.AdoptableLabels(existingAuthDelegatorClusterRoleBinding.GetLabels(), true, i.owner) {
			logger.WithFields(log.Fields{"obj": "authDelegatorCRB", "labels": existingAuthDelegatorClusterRoleBinding.GetLabels()}).Debug("adopting")
			if err := ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, i.owner); err != nil {
				return nil, err
			}
		}

		// Attempt an update.
		if _, err := i.strategyClient.GetOpClient().UpdateClusterRoleBinding(authDelegatorClusterRoleBinding); err != nil {
			logger.Warnf("could not update auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role.
		if err := ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, i.owner); err != nil {
			return nil, err
		}
		_, err = i.strategyClient.GetOpClient().CreateClusterRoleBinding(authDelegatorClusterRoleBinding)
		if err != nil {
			log.Warnf("could not create auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, err
		}
	} else {
		return nil, err
	}

	// Create RoleBinding to extension-apiserver-authentication-reader Role in the kube-system namespace.
	authReaderRoleBinding := &rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      depSpec.Template.Spec.ServiceAccountName,
				Namespace: i.owner.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "extension-apiserver-authentication-reader",
		},
	}
	authReaderRoleBinding.SetName(service.GetName() + "-auth-reader")
	authReaderRoleBinding.SetNamespace(kubeSystem)

	existingAuthReaderRoleBinding, err := i.strategyClient.GetOpLister().RbacV1().RoleBindingLister().RoleBindings(kubeSystem).Get(authReaderRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain.
		if ownerutil.AdoptableLabels(existingAuthReaderRoleBinding.GetLabels(), true, i.owner) {
			logger.WithFields(log.Fields{"obj": "existingAuthReaderRB", "labels": existingAuthReaderRoleBinding.GetLabels()}).Debug("adopting")
			if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, i.owner); err != nil {
				return nil, err
			}
		}
		// Attempt an update.
		if _, err := i.strategyClient.GetOpClient().UpdateRoleBinding(authReaderRoleBinding); err != nil {
			logger.Warnf("could not update auth reader role binding %s", authReaderRoleBinding.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role.
		if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, i.owner); err != nil {
			return nil, err
		}
		_, err = i.strategyClient.GetOpClient().CreateRoleBinding(authReaderRoleBinding)
		if err != nil {
			log.Warnf("could not create auth reader role binding %s", authReaderRoleBinding.GetName())
			return nil, err
		}
	} else {
		return nil, err
	}

	// Update deployment with secret volume mount.
	volume := corev1.Volume{
		Name: "apiservice-cert",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secret.GetName(),
				Items: []corev1.KeyToPath{
					{
						Key:  "tls.crt",
						Path: "apiserver.crt",
					},
					{
						Key:  "tls.key",
						Path: "apiserver.key",
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
		MountPath: "/apiserver.local.config/certificates",
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
	// is used by the apiserver if not hot reloading.
	depSpec.Template.ObjectMeta.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	return &depSpec, nil
}

func (i *StrategyDeploymentInstaller) createOrUpdateAPIService(caPEM []byte, desc v1alpha1.APIServiceDescription) error {
	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
	logger := log.WithFields(log.Fields{
		"owner":      i.owner.GetName(),
		"namespace":  i.owner.GetNamespace(),
		"apiservice": fmt.Sprintf("%s.%s", desc.Version, desc.Group),
	})

	exists := true
	apiService, err := i.strategyClient.GetOpLister().APIRegistrationV1().APIServiceLister().Get(apiServiceName)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		exists = false
		apiService = &apiregistrationv1.APIService{
			Spec: apiregistrationv1.APIServiceSpec{
				Group:                desc.Group,
				Version:              desc.Version,
				GroupPriorityMinimum: int32(2000),
				VersionPriority:      int32(15),
			},
		}
		apiService.SetName(apiServiceName)
	} else {
		csv, ok := i.owner.(*v1alpha1.ClusterServiceVersion)
		if !ok {
			return fmt.Errorf("APIServices require a CSV Owner.")
		}

		adoptable, err := IsAPIServiceAdoptable(i.strategyClient.GetOpLister(), csv, apiService)
		if err != nil {
			logger.WithFields(log.Fields{"obj": "apiService", "labels": apiService.GetLabels()}).Errorf("adoption check failed - %v", err)
		}

		if !adoptable {
			return fmt.Errorf("pre-existing APIService %s.%s is not adoptable", desc.Version, desc.Group)
		}
	}

	// Add the CSV as an owner
	if err := ownerutil.AddOwnerLabels(apiService, i.owner); err != nil {
		return err
	}

	// Create a service for the deployment
	containerPort := int32(443)
	if desc.ContainerPort > 0 {
		containerPort = desc.ContainerPort
	}
	// update the ServiceReference
	apiService.Spec.Service = &apiregistrationv1.ServiceReference{
		Namespace: i.owner.GetNamespace(),
		Name:      serviceName(desc.DeploymentName),
		Port:      &containerPort,
	}

	// create a fresh CA bundle
	apiService.Spec.CABundle = caPEM

	// attempt a update or create
	if exists {
		logger.Debug("updating APIService")
		_, err = i.strategyClient.GetOpClient().UpdateAPIService(apiService)
	} else {
		logger.Debug("creating APIService")
		_, err = i.strategyClient.GetOpClient().CreateAPIService(apiService)
	}

	if err != nil {
		logger.Warnf("could not create or update APIService")
		return err
	}

	return nil
}

func IsAPIServiceAdoptable(opLister operatorlister.OperatorLister, target *v1alpha1.ClusterServiceVersion, apiService *apiregistrationv1.APIService) (adoptable bool, err error) {
	if apiService == nil || target == nil {
		err = errors.New("invalid input")
		return
	}

	labels := apiService.GetLabels()
	ownerKind := labels[ownerutil.OwnerKind]
	ownerName := labels[ownerutil.OwnerKey]
	ownerNamespace := labels[ownerutil.OwnerNamespaceKey]

	if ownerKind == "" || ownerNamespace == "" || ownerName == "" {
		return
	}

	if err := ownerutil.InferGroupVersionKind(target); err != nil {
		log.Warn(err.Error())
	}

	targetKind := target.GetObjectKind().GroupVersionKind().Kind
	if ownerKind != targetKind {
		return
	}

	// Get the CSV that target replaces
	replacing, replaceGetErr := opLister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(target.GetNamespace()).Get(target.Spec.Replaces)
	if replaceGetErr != nil && !k8serrors.IsNotFound(replaceGetErr) && !k8serrors.IsGone(replaceGetErr) {
		err = replaceGetErr
		return
	}

	// Get the current owner CSV of the APIService
	currentOwnerCSV, ownerGetErr := opLister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ownerNamespace).Get(ownerName)
	if ownerGetErr != nil && !k8serrors.IsNotFound(ownerGetErr) && !k8serrors.IsGone(ownerGetErr) {
		err = ownerGetErr
		return
	}

	owners := []ownerutil.Owner{target}
	if replacing != nil {
		owners = append(owners, replacing)
	}
	if currentOwnerCSV != nil && (currentOwnerCSV.Status.Phase == v1alpha1.CSVPhaseReplacing || currentOwnerCSV.Status.Phase == v1alpha1.CSVPhaseDeleting) {
		owners = append(owners, currentOwnerCSV)
	}

	adoptable = ownerutil.AdoptableLabels(apiService.GetLabels(), true, owners...)
	return
}

func IsAPIServiceAvailable(apiService *apiregistrationv1.APIService) bool {
	for _, c := range apiService.Status.Conditions {
		if c.Type == apiregistrationv1.Available && c.Status == apiregistrationv1.ConditionTrue {
			return true
		}
	}
	return false
}

// deleteLegacyAPIServiceResources deletes resources that were created by OLM for an APIService that used the old naming convention.
func (i *StrategyDeploymentInstaller) deleteLegacyAPIServiceResources(desc installerAPIServiceDescriptions) error {
	logger := log.WithFields(log.Fields{
		"ownerName":      i.owner.GetName(),
		"ownerNamespace": i.owner.GetNamespace(),
		"ownerKind":      i.owner.GetObjectKind().GroupVersionKind().GroupKind().Kind,
	})
	namespace := i.owner.GetNamespace()
	apiServiceName := fmt.Sprintf("%s.%s", desc.apiServiceDescription.Version, desc.apiServiceDescription.Group)

	// Handle an edgecase where the legacy resources names matches the new names.
	// This only occurs when the Group of the description matches the name of the deployment
	// and the version is equal to "service".
	if legacyAPIServiceNameToServiceName(apiServiceName) == serviceName(desc.apiServiceDescription.DeploymentName) {
		return nil
	}

	// Handle an edgecase where the legacy resources names matches the new names.
	// This only occurs when the version of the description matches the name of the deployment
	// and the group is equal to "service"
	// If the names match, do not delete the service as OLM has already updated it.
	legacyServiceName := legacyAPIServiceNameToServiceName(apiServiceName)
	if legacyServiceName != serviceName(desc.apiServiceDescription.DeploymentName) {
		// Attempt to delete the legacy Service.
		existingService, err := i.strategyClient.GetOpClient().GetService(namespace, legacyServiceName)
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return err
			}
		} else if ownerutil.AdoptableLabels(existingService.GetLabels(), true, i.owner) {
			logger.Infof("Deleting Service with legacy APIService name %s", existingService.Name)
			err = i.strategyClient.GetOpClient().DeleteService(namespace, legacyServiceName, &metav1.DeleteOptions{})
			if err != nil && !k8serrors.IsNotFound(err) {
				return err
			}
		} else {
			logger.Infof("Service with legacy APIService resource name %s not adoptable", existingService.Name)
		}
	} else {
		logger.Infof("New Service name matches legacy APIService resource name %s", legacyServiceName)
	}

	// Attempt to delete the legacy Secret.
	existingSecret, err := i.strategyClient.GetOpClient().GetSecret(namespace, secretName(apiServiceName))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingSecret.GetLabels(), true, i.owner) {
		logger.Infof("Deleting Secret with legacy APIService name %s", existingSecret.Name)
		err = i.strategyClient.GetOpClient().DeleteSecret(namespace, secretName(apiServiceName), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("Secret with legacy APIService  %s not adoptable", existingSecret.Name)
	}

	// Attempt to delete the legacy Role.
	existingRole, err := i.strategyClient.GetOpClient().GetRole(namespace, secretName(apiServiceName))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingRole.GetLabels(), true, i.owner) {
		logger.Infof("Deleting Role with legacy APIService name %s", existingRole.Name)
		err = i.strategyClient.GetOpClient().DeleteRole(namespace, secretName(apiServiceName), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("Role with legacy APIService name %s not adoptable", existingRole.Name)
	}

	// Attempt to delete the legacy secret RoleBinding.
	existingRoleBinding, err := i.strategyClient.GetOpClient().GetRoleBinding(namespace, secretName(apiServiceName))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingRoleBinding.GetLabels(), true, i.owner) {
		logger.Infof("Deleting RoleBinding with legacy APIService name %s", existingRoleBinding.Name)
		err = i.strategyClient.GetOpClient().DeleteRoleBinding(namespace, secretName(apiServiceName), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("RoleBinding with legacy APIService name %s not adoptable", existingRoleBinding.Name)
	}

	// Attempt to delete the legacy ClusterRoleBinding.
	existingClusterRoleBinding, err := i.strategyClient.GetOpClient().GetClusterRoleBinding(apiServiceName + "-system:auth-delegator")
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingClusterRoleBinding.GetLabels(), true, i.owner) {
		logger.Infof("Deleting ClusterRoleBinding with legacy APIService name %s", existingClusterRoleBinding.Name)
		err = i.strategyClient.GetOpClient().DeleteClusterRoleBinding(apiServiceName+"-system:auth-delegator", &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("ClusterRoleBinding with legacy APIService name %s not adoptable", existingClusterRoleBinding.Name)
	}

	// Attempt to delete the legacy AuthReadingRoleBinding.
	existingRoleBinding, err = i.strategyClient.GetOpClient().GetRoleBinding(kubeSystem, apiServiceName+"-auth-reader")
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingRoleBinding.GetLabels(), true, i.owner) {
		logger.Infof("Deleting RoleBinding with legacy APIService name %s", existingRoleBinding.Name)
		err = i.strategyClient.GetOpClient().DeleteRoleBinding(kubeSystem, apiServiceName+"-auth-reader", &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("RoleBinding with legacy APIService name %s not adoptable", existingRoleBinding.Name)
	}

	return nil
}

// legacyAPIServiceNameToServiceName returns the result of replacing all
// periods in the given APIService name with hyphens
func legacyAPIServiceNameToServiceName(apiServiceName string) string {
	// Replace all '.'s with "-"s to convert to a DNS-1035 label
	return strings.Replace(apiServiceName, ".", "-", -1)
}
