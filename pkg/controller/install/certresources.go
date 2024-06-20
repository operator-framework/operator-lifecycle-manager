package install

import (
	"fmt"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"

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
	// olm managed label
	OLMManagedLabelKey   = "olm.managed"
	OLMManagedLabelValue = "true"
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

func HostnamesForService(serviceName, namespace string) []string {
	return []string{
		fmt.Sprintf("%s.%s", serviceName, namespace),
		fmt.Sprintf("%s.%s.svc", serviceName, namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace),
	}
}

func (i *StrategyDeploymentInstaller) getCertResources() []certResource {
	return append(i.apiServiceDescriptions, i.webhookDescriptions...)
}

func (i *StrategyDeploymentInstaller) certResourcesForDeployment(deploymentName string) []certResource {
	var result []certResource
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
	i.certificateExpirationTime = CalculateCertExpiration(time.Now())
	ca, err := certs.GenerateCA(i.certificateExpirationTime, Organization)
	if err != nil {
		logger.Debug("failed to generate CA")
		return nil, err
	}

	for n, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		certResources := i.certResourcesForDeployment(sddSpec.Name)

		if len(certResources) == 0 {
			log.Info("No api or webhook descs to add CA to")
			continue
		}

		// Update the deployment for each certResource
		newDepSpec, caPEM, err := i.installCertRequirementsForDeployment(sddSpec.Name, ca, i.certificateExpirationTime, sddSpec.Spec, getServicePorts(certResources))
		if err != nil {
			return nil, err
		}

		i.updateCertResourcesForDeployment(sddSpec.Name, caPEM)

		strategyDetailsDeployment.DeploymentSpecs[n].Spec = *newDepSpec
	}
	return strategyDetailsDeployment, nil
}

func (i *StrategyDeploymentInstaller) CertsRotateAt() time.Time {
	return CalculateCertRotatesAt(i.certificateExpirationTime)
}

func (i *StrategyDeploymentInstaller) CertsRotated() bool {
	return i.certificatesRotated
}

// shouldRotateCerts indicates whether an apiService cert should be rotated due to being
// malformed, invalid, expired, inactive or within a specific freshness interval (DefaultCertMinFresh) before expiry.
func shouldRotateCerts(certSecret *corev1.Secret, hosts []string) bool {
	now := metav1.Now()
	caPEM, ok := certSecret.Data[OLMCAPEMKey]
	if !ok {
		// missing CA cert in secret
		return true
	}
	certPEM, ok := certSecret.Data["tls.crt"]
	if !ok {
		// missing cert in secret
		return true
	}

	ca, err := certs.PEMToCert(caPEM)
	if err != nil {
		// malformed CA cert
		return true
	}
	cert, err := certs.PEMToCert(certPEM)
	if err != nil {
		// malformed cert
		return true
	}

	// check for cert freshness
	if !certs.Active(ca) || now.Time.After(CalculateCertRotatesAt(ca.NotAfter)) ||
		!certs.Active(cert) || now.Time.After(CalculateCertRotatesAt(cert.NotAfter)) {
		return true
	}

	// Check validity of serving cert and if serving cert is trusted by the CA
	for _, host := range hosts {
		if err := certs.VerifyCert(ca, cert, host); err != nil {
			return true
		}
	}
	return false
}

func (i *StrategyDeploymentInstaller) ShouldRotateCerts(s Strategy) (bool, error) {
	strategy, ok := s.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return false, fmt.Errorf("failed to install %s strategy with deployment installer: unsupported deployment install strategy", strategy.GetStrategyName())
	}

	hasCerts := sets.New[string]()
	for _, c := range i.getCertResources() {
		hasCerts.Insert(c.getDeploymentName())
	}
	for _, sddSpec := range strategy.DeploymentSpecs {
		if hasCerts.Has(sddSpec.Name) {
			certSecret, err := i.strategyClient.GetOpLister().CoreV1().SecretLister().Secrets(i.owner.GetNamespace()).Get(SecretName(ServiceName(sddSpec.Name)))
			if err == nil {
				if shouldRotateCerts(certSecret, HostnamesForService(ServiceName(sddSpec.Name), i.owner.GetNamespace())) {
					return true, nil
				}
			} else if apierrors.IsNotFound(err) {
				return true, nil
			} else {
				return false, err
			}
		}
	}
	return false, nil
}

func CalculateCertExpiration(startingFrom time.Time) time.Time {
	return startingFrom.Add(DefaultCertValidFor)
}

func CalculateCertRotatesAt(certExpirationTime time.Time) time.Time {
	return certExpirationTime.Add(-1 * DefaultCertMinFresh)
}

func (i *StrategyDeploymentInstaller) installCertRequirementsForDeployment(deploymentName string, ca *certs.KeyPair, expiration time.Time, depSpec appsv1.DeploymentSpec, ports []corev1.ServicePort) (*appsv1.DeploymentSpec, []byte, error) {
	logger := log.WithFields(log.Fields{})

	// apply Service
	serviceName := ServiceName(deploymentName)
	portsApplyConfig := []*corev1ac.ServicePortApplyConfiguration{}
	for _, p := range ports {
		ac := corev1ac.ServicePort().
			WithName(p.Name).
			WithPort(p.Port).
			WithTargetPort(p.TargetPort)
		portsApplyConfig = append(portsApplyConfig, ac)
	}

	svcApplyConfig := corev1ac.Service(serviceName, i.owner.GetNamespace()).
		WithSpec(corev1ac.ServiceSpec().
			WithPorts(portsApplyConfig...).
			WithSelector(depSpec.Selector.MatchLabels)).
		WithOwnerReferences(ownerutil.NonBlockingOwnerApplyConfiguration(i.owner)).
		WithLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

	if _, err := i.strategyClient.GetOpClient().ApplyService(svcApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}); err != nil {
		log.Errorf("could not apply service %s: %s", *svcApplyConfig.Name, err.Error())
		return nil, nil, err
	}

	// Create signed serving cert
	hosts := HostnamesForService(serviceName, i.owner.GetNamespace())
	servingPair, err := certGenerator.Generate(expiration, Organization, ca, hosts)
	if err != nil {
		logger.Warnf("could not generate signed certs for hosts %v", hosts)
		return nil, nil, err
	}

	// Create Secret for serving cert
	certPEM, privPEM, err := servingPair.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert serving certificate and private key to PEM format for Service %s", serviceName)
		return nil, nil, err
	}

	// Add olmcahash as a label to the caPEM
	caPEM, _, err := ca.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert CA certificate to PEM format for Service %s", serviceName)
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
	secret.SetName(SecretName(serviceName))
	secret.SetNamespace(i.owner.GetNamespace())
	secret.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})
	secret.SetLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

	existingSecret, err := i.strategyClient.GetOpLister().CoreV1().SecretLister().Secrets(i.owner.GetNamespace()).Get(secret.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(i.owner, existingSecret.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secret, i.owner)
		}

		// Attempt an update
		// TODO: Check that the secret was not modified
		if !shouldRotateCerts(existingSecret, HostnamesForService(serviceName, i.owner.GetNamespace())) {
			logger.Warnf("reusing existing cert %s", secret.GetName())
			secret = existingSecret
			caPEM = existingSecret.Data[OLMCAPEMKey]
			caHash = certs.PEMSHA256(caPEM)
		} else {
			if _, err := i.strategyClient.GetOpClient().UpdateSecret(secret); err != nil {
				logger.Warnf("could not update secret %s", secret.GetName())
				return nil, nil, err
			}
			i.certificatesRotated = true
		}
	} else if apierrors.IsNotFound(err) {
		// Create the secret
		ownerutil.AddNonBlockingOwner(secret, i.owner)
		if _, err := i.strategyClient.GetOpClient().CreateSecret(secret); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				log.Warnf("could not create secret %s: %v", secret.GetName(), err)
				return nil, nil, err
			}
			// if the secret isn't in the cache but exists in the cluster, it's missing the labels for the cache filter
			// and just needs to be updated
			if _, err := i.strategyClient.GetOpClient().UpdateSecret(secret); err != nil {
				log.Warnf("could not update secret %s: %v", secret.GetName(), err)
				return nil, nil, err
			}
		}
		i.certificatesRotated = true
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
	secretRole.SetLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

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
	} else if apierrors.IsNotFound(err) {
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
	secretRoleBinding.SetLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

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
	} else if apierrors.IsNotFound(err) {
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

	// apply ClusterRoleBinding to system:auth-delegator Role
	crbLabels := map[string]string{OLMManagedLabelKey: OLMManagedLabelValue}
	for key, val := range ownerutil.OwnerLabel(i.owner, i.owner.GetObjectKind().GroupVersionKind().Kind) {
		crbLabels[key] = val
	}
	crbApplyConfig := rbacv1ac.ClusterRoleBinding(AuthDelegatorClusterRoleBindingName(serviceName)).
		WithSubjects(rbacv1ac.Subject().
			WithKind("ServiceAccount").
			WithAPIGroup("").
			WithName(depSpec.Template.Spec.ServiceAccountName).
			WithNamespace(i.owner.GetNamespace())).
		WithRoleRef(rbacv1ac.RoleRef().
			WithAPIGroup("rbac.authorization.k8s.io").
			WithKind("ClusterRole").
			WithName("system:auth-delegator")).
		WithLabels(crbLabels)

	if _, err = i.strategyClient.GetOpClient().ApplyClusterRoleBinding(crbApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}); err != nil {
		log.Errorf("could not apply auth delegator clusterrolebinding %s: %s", *crbApplyConfig.Name, err.Error())
		return nil, nil, err
	}

	// Apply RoleBinding to extension-apiserver-authentication-reader Role in the kube-system namespace.
	authReaderRoleBindingApplyConfig := rbacv1ac.RoleBinding(AuthReaderRoleBindingName(serviceName), KubeSystem).
		WithLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue}).
		WithSubjects(rbacv1ac.Subject().
			WithKind("ServiceAccount").
			WithAPIGroup("").
			WithName(depSpec.Template.Spec.ServiceAccountName).
			WithNamespace(i.owner.GetNamespace())).
		WithRoleRef(rbacv1ac.RoleRef().
			WithAPIGroup("rbac.authorization.k8s.io").
			WithKind("Role").
			WithName("extension-apiserver-authentication-reader"))

	if _, err = i.strategyClient.GetOpClient().ApplyRoleBinding(authReaderRoleBindingApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}); err != nil {
		log.Errorf("could not apply auth reader rolebinding %s: %s", *authReaderRoleBindingApplyConfig.Name, err.Error())
		return nil, nil, err
	}

	AddDefaultCertVolumeAndVolumeMounts(&depSpec, secret.GetName())

	// Setting the olm hash label forces a rollout and ensures that the new secret
	// is used by the apiserver if not hot reloading.
	SetCAAnnotation(&depSpec, caHash)
	return &depSpec, caPEM, nil
}

func AuthDelegatorClusterRoleBindingName(serviceName string) string {
	return serviceName + "-system:auth-delegator"
}

func AuthReaderRoleBindingName(serviceName string) string {
	return serviceName + "-auth-reader"
}

func SetCAAnnotation(depSpec *appsv1.DeploymentSpec, caHash string) {
	if len(depSpec.Template.ObjectMeta.GetAnnotations()) == 0 {
		depSpec.Template.ObjectMeta.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})
	} else {
		depSpec.Template.Annotations[OLMCAHashAnnotationKey] = caHash
	}
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
