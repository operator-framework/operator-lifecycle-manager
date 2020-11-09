package install

import (
	"fmt"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

var _ certResource = &apiServiceDescriptionsWithCAPEM{}

var _ certResource = &webhookDescriptionWithCAPEM{}

// TODO: to keep refactoring minimal for backports, this is factored out here so that it can be replaced
// during tests. but it should be properly injected instead.
var certGenerator certs.CertGenerator = certs.CertGeneratorFunc(certs.CreateSignedServingPair)

const (
	// DefaultCertMinFresh is the default min-fresh value - 1 day
	DefaultCertMinFresh = time.Hour * 24
	// DefaultCertValidFor is the default duration a cert can be valid for - 2 years
	DefaultCertValidFor = time.Hour * 24 * 730
	// OLMCAPEMKey is the CAPEM
	OLMCAPEMKey = "olmCAKey"
	// OLMCAHashAnnotationKey is the label key used to store the hash of the CA cert
	OLMCAHashAnnotationKey = "olmcahash"
	// Organization is the organization name used in the generation of x509 certs
	Organization = "Red Hat, Inc."
	// Kubernetes System namespace
	KubeSystem = "kube-system"
)

type certResource interface {
	getName() string
	setCAPEM(caPEM []byte)
	getCAPEM() []byte
	getServicePort() corev1.ServicePort
	getDeploymentName() string
}

func getServicePorts(descs []certResource) []corev1.ServicePort {
	result := []corev1.ServicePort{}
	for _, desc := range descs {
		if !containsServicePort(result, desc.getServicePort()) {
			result = append(result, desc.getServicePort())
		}
	}

	return result
}

func containsServicePort(servicePorts []corev1.ServicePort, targetServicePort corev1.ServicePort) bool {
	for _, servicePort := range servicePorts {
		if servicePort == targetServicePort {
			return true
		}
	}

	return false
}

type apiServiceDescriptionsWithCAPEM struct {
	apiServiceDescription v1alpha1.APIServiceDescription
	caPEM                 []byte
}

func (i *apiServiceDescriptionsWithCAPEM) getName() string {
	return i.apiServiceDescription.Name
}

func (i *apiServiceDescriptionsWithCAPEM) setCAPEM(caPEM []byte) {
	i.caPEM = caPEM
}

func (i *apiServiceDescriptionsWithCAPEM) getCAPEM() []byte {
	return i.caPEM
}

func (i *apiServiceDescriptionsWithCAPEM) getDeploymentName() string {
	return i.apiServiceDescription.DeploymentName
}

func (i *apiServiceDescriptionsWithCAPEM) getServicePort() corev1.ServicePort {
	containerPort := 443
	if i.apiServiceDescription.ContainerPort > 0 {
		containerPort = int(i.apiServiceDescription.ContainerPort)
	}
	return corev1.ServicePort{
		Name:       strconv.Itoa(containerPort),
		Port:       int32(containerPort),
		TargetPort: intstr.FromInt(containerPort),
	}
}

type webhookDescriptionWithCAPEM struct {
	webhookDescription v1alpha1.WebhookDescription
	caPEM              []byte
}

func (i *webhookDescriptionWithCAPEM) getName() string {
	return i.webhookDescription.GenerateName
}

func (i *webhookDescriptionWithCAPEM) setCAPEM(caPEM []byte) {
	i.caPEM = caPEM
}

func (i *webhookDescriptionWithCAPEM) getCAPEM() []byte {
	return i.caPEM
}

func (i *webhookDescriptionWithCAPEM) getDeploymentName() string {
	return i.webhookDescription.DeploymentName
}

func (i *webhookDescriptionWithCAPEM) getServicePort() corev1.ServicePort {
	containerPort := 443
	if i.webhookDescription.ContainerPort > 0 {
		containerPort = int(i.webhookDescription.ContainerPort)
	}

	// Before users could set TargetPort in the CSV, OLM just set its
	// value to the containerPort. This change keeps OLM backwards compatible
	// if the TargetPort is not set in the CSV.
	targetPort := intstr.FromInt(containerPort)
	if i.webhookDescription.TargetPort != nil {
		targetPort = *i.webhookDescription.TargetPort
	}
	return corev1.ServicePort{
		Name:       strconv.Itoa(containerPort),
		Port:       int32(containerPort),
		TargetPort: targetPort,
	}
}

func SecretName(serviceName string) string {
	return serviceName + "-cert"
}

func ServiceName(deploymentName string) string {
	return deploymentName + "-service"
}

func (i *StrategyDeploymentInstaller) getCertResources() []certResource {
	return append(i.apiServiceDescriptions, i.webhookDescriptions...)
}

func (i *StrategyDeploymentInstaller) certResourcesForDeployment(deploymentName string) []certResource {
	result := []certResource{}
	for _, desc := range i.getCertResources() {
		if desc.getDeploymentName() == deploymentName {
			result = append(result, desc)
		}
	}
	return result
}

func (i *StrategyDeploymentInstaller) updateCertResourcesForDeployment(deploymentName string, caPEM []byte) {
	for _, desc := range i.certResourcesForDeployment(deploymentName) {
		desc.setCAPEM(caPEM)
	}
}

func (i *StrategyDeploymentInstaller) installCertRequirements(strategy Strategy) (*v1alpha1.StrategyDetailsDeployment, error) {
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
		certResources := i.certResourcesForDeployment(sddSpec.Name)

		if len(certResources) == 0 {
			log.Info("No api or webhook descs to add CA to")
			continue
		}

		// Update the deployment for each certResource
		newDepSpec, caPEM, err := i.installCertRequirementsForDeployment(sddSpec.Name, ca, rotateAt, sddSpec.Spec, getServicePorts(certResources))
		if err != nil {
			return nil, err
		}

		i.updateCertResourcesForDeployment(sddSpec.Name, caPEM)

		strategyDetailsDeployment.DeploymentSpecs[n].Spec = *newDepSpec
	}
	return strategyDetailsDeployment, nil
}

func ShouldRotateCerts(csv *v1alpha1.ClusterServiceVersion) bool {
	now := metav1.Now()
	if !csv.Status.CertsRotateAt.IsZero() && csv.Status.CertsRotateAt.Before(&now) {
		return true
	}

	return false
}

func (i *StrategyDeploymentInstaller) installCertRequirementsForDeployment(deploymentName string, ca *certs.KeyPair, rotateAt time.Time, depSpec appsv1.DeploymentSpec, ports []corev1.ServicePort) (*appsv1.DeploymentSpec, []byte, error) {
	logger := log.WithFields(log.Fields{})

	// Create a service for the deployment
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports:    ports,
			Selector: depSpec.Selector.MatchLabels,
		},
	}
	service.SetName(ServiceName(deploymentName))
	service.SetNamespace(i.owner.GetNamespace())
	ownerutil.AddNonBlockingOwner(service, i.owner)

	existingService, err := i.strategyClient.GetOpLister().CoreV1().ServiceLister().Services(i.owner.GetNamespace()).Get(service.GetName())
	if err == nil {
		if !ownerutil.Adoptable(i.owner, existingService.GetOwnerReferences()) {
			return nil, nil, fmt.Errorf("service %s not safe to replace: extraneous ownerreferences found", service.GetName())
		}
		service.SetOwnerReferences(existingService.GetOwnerReferences())

		// Delete the Service to replace
		deleteErr := i.strategyClient.GetOpClient().DeleteService(service.GetNamespace(), service.GetName(), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(deleteErr) {
			return nil, nil, fmt.Errorf("could not delete existing service %s", service.GetName())
		}
	}

	// Attempt to create the Service
	_, err = i.strategyClient.GetOpClient().CreateService(service)
	if err != nil {
		logger.Warnf("could not create service %s", service.GetName())
		return nil, nil, fmt.Errorf("could not create service %s: %s", service.GetName(), err.Error())
	}

	// Create signed serving cert
	hosts := []string{
		fmt.Sprintf("%s.%s", service.GetName(), i.owner.GetNamespace()),
		fmt.Sprintf("%s.%s.svc", service.GetName(), i.owner.GetNamespace()),
	}
	servingPair, err := certGenerator.Generate(rotateAt, Organization, ca, hosts)
	if err != nil {
		logger.Warnf("could not generate signed certs for hosts %v", hosts)
		return nil, nil, err
	}

	// Create Secret for serving cert
	certPEM, privPEM, err := servingPair.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert serving certificate and private key to PEM format for Service %s", service.GetName())
		return nil, nil, err
	}

	// Add olmcahash as a label to the caPEM
	caPEM, _, err := ca.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert CA certificate to PEM format for Service %s", service)
		return nil, nil, err
	}
	caHash := certs.PEMSHA256(caPEM)

	secret := &corev1.Secret{
		Data: map[string][]byte{
			"tls.crt":   certPEM,
			"tls.key":   privPEM,
			OLMCAPEMKey: caPEM,
		},
		Type: corev1.SecretTypeTLS,
	}
	secret.SetName(SecretName(service.GetName()))
	secret.SetNamespace(i.owner.GetNamespace())
	secret.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	existingSecret, err := i.strategyClient.GetOpLister().CoreV1().SecretLister().Secrets(i.owner.GetNamespace()).Get(secret.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(i.owner, existingSecret.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secret, i.owner)
		}

		// Attempt an update
		// TODO: Check that the secret was not modified
		if existingCAPEM, ok := existingSecret.Data[OLMCAPEMKey]; ok && !ShouldRotateCerts(i.owner.(*v1alpha1.ClusterServiceVersion)) {
			logger.Warnf("reusing existing cert %s", secret.GetName())
			secret = existingSecret
			caPEM = existingCAPEM
			caHash = certs.PEMSHA256(caPEM)
		} else if _, err := i.strategyClient.GetOpClient().UpdateSecret(secret); err != nil {
			logger.Warnf("could not update secret %s", secret.GetName())
			return nil, nil, err
		}

	} else if k8serrors.IsNotFound(err) {
		// Create the secret
		ownerutil.AddNonBlockingOwner(secret, i.owner)
		_, err = i.strategyClient.GetOpClient().CreateSecret(secret)
		if err != nil {
			log.Warnf("could not create secret %s", secret.GetName())
			return nil, nil, err
		}
	} else {
		return nil, nil, err
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
			return nil, nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRole, i.owner)
		_, err = i.strategyClient.GetOpClient().CreateRole(secretRole)
		if err != nil {
			log.Warnf("could not create secret role %s", secretRole.GetName())
			return nil, nil, err
		}
	} else {
		return nil, nil, err
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
			return nil, nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRoleBinding, i.owner)
		_, err = i.strategyClient.GetOpClient().CreateRoleBinding(secretRoleBinding)
		if err != nil {
			log.Warnf("could not create secret rolebinding with dep spec: %#v", depSpec)
			return nil, nil, err
		}
	} else {
		return nil, nil, err
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
				return nil, nil, err
			}
		}

		// Attempt an update.
		if _, err := i.strategyClient.GetOpClient().UpdateClusterRoleBinding(authDelegatorClusterRoleBinding); err != nil {
			logger.Warnf("could not update auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role.
		if err := ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, i.owner); err != nil {
			return nil, nil, err
		}
		_, err = i.strategyClient.GetOpClient().CreateClusterRoleBinding(authDelegatorClusterRoleBinding)
		if err != nil {
			log.Warnf("could not create auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, nil, err
		}
	} else {
		return nil, nil, err
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
	authReaderRoleBinding.SetNamespace(KubeSystem)

	existingAuthReaderRoleBinding, err := i.strategyClient.GetOpLister().RbacV1().RoleBindingLister().RoleBindings(KubeSystem).Get(authReaderRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain.
		if ownerutil.AdoptableLabels(existingAuthReaderRoleBinding.GetLabels(), true, i.owner) {
			logger.WithFields(log.Fields{"obj": "existingAuthReaderRB", "labels": existingAuthReaderRoleBinding.GetLabels()}).Debug("adopting")
			if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, i.owner); err != nil {
				return nil, nil, err
			}
		}
		// Attempt an update.
		if _, err := i.strategyClient.GetOpClient().UpdateRoleBinding(authReaderRoleBinding); err != nil {
			logger.Warnf("could not update auth reader role binding %s", authReaderRoleBinding.GetName())
			return nil, nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role.
		if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, i.owner); err != nil {
			return nil, nil, err
		}
		_, err = i.strategyClient.GetOpClient().CreateRoleBinding(authReaderRoleBinding)
		if err != nil {
			log.Warnf("could not create auth reader role binding %s", authReaderRoleBinding.GetName())
			return nil, nil, err
		}
	} else {
		return nil, nil, err
	}
	AddDefaultCertVolumeAndVolumeMounts(&depSpec, secret.GetName())

	// Setting the olm hash label forces a rollout and ensures that the new secret
	// is used by the apiserver if not hot reloading.
	depSpec.Template.ObjectMeta.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	return &depSpec, caPEM, nil
}

// AddDefaultCertVolumeAndVolumeMounts mounts the CA Cert generated by OLM to the location that OLM expects
// APIService certs to be as well as the location that the Operator-SDK and Kubebuilder expect webhook
// certs to be.
func AddDefaultCertVolumeAndVolumeMounts(depSpec *appsv1.DeploymentSpec, secretName string) {
	// Update deployment with secret volume mount.
	volume := corev1.Volume{
		Name: "apiservice-cert",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
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

	mount := corev1.VolumeMount{
		Name:      volume.Name,
		MountPath: "/apiserver.local.config/certificates",
	}

	addCertVolumeAndVolumeMount(depSpec, volume, mount)

	volume = corev1.Volume{
		Name: "webhook-cert",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
				Items: []corev1.KeyToPath{
					{
						Key:  "tls.crt",
						Path: "tls.crt",
					},
					{
						Key:  "tls.key",
						Path: "tls.key",
					},
				},
			},
		},
	}

	mount = corev1.VolumeMount{
		Name:      volume.Name,
		MountPath: "/tmp/k8s-webhook-server/serving-certs",
	}
	addCertVolumeAndVolumeMount(depSpec, volume, mount)
}

func addCertVolumeAndVolumeMount(depSpec *appsv1.DeploymentSpec, volume corev1.Volume, volumeMount corev1.VolumeMount) {
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

	for i, container := range depSpec.Template.Spec.Containers {
		found := false
		for j, m := range container.VolumeMounts {
			if m.Name == volumeMount.Name {
				found = true
				break
			}

			// Replace if mounting to the same location.
			if m.MountPath == volumeMount.MountPath {
				container.VolumeMounts[j] = volumeMount
				found = true
				break
			}
		}
		if !found {
			container.VolumeMounts = append(container.VolumeMounts, volumeMount)
		}

		depSpec.Template.Spec.Containers[i] = container
	}
}
