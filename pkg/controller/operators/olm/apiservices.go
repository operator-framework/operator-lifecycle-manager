package olm

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
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
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

func (a *Operator) shouldRotateCerts(csv *v1alpha1.ClusterServiceVersion) bool {
	now := metav1.Now()
	if !csv.Status.CertsRotateAt.IsZero() && csv.Status.CertsRotateAt.Before(&now) {
		return true
	}

	return false
}

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
		adoptable, err := a.isAPIServiceAdoptable(csv, apiService)
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

		serviceName := serviceName(desc.DeploymentName)
		service, err := a.lister.CoreV1().ServiceLister().Services(csv.GetNamespace()).Get(serviceName)
		if err != nil {
			logger.WithField("service", serviceName).Warnf("could not retrieve generated Service")
			errs = append(errs, err)
			continue
		}

		// Check if the APIService points to the correct service
		if apiService.Spec.Service.Name != serviceName || apiService.Spec.Service.Namespace != csv.GetNamespace() {
			logger.WithFields(log.Fields{"service": apiService.Spec.Service.Name, "serviceNamespace": apiService.Spec.Service.Namespace}).Warnf("APIService service reference mismatch")
			errs = append(errs, fmt.Errorf("APIService service reference mismatch"))
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
			errs = append(errs, fmt.Errorf("CA cert not active"))
			continue
		}

		// Check if serving cert is active
		secretName := secretName(serviceName)
		secret, err := a.lister.CoreV1().SecretLister().Secrets(csv.GetNamespace()).Get(secretName)
		if err != nil {
			logger.WithField("secret", secretName).Warnf("could not retrieve generated Secret")
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
			errs = append(errs, fmt.Errorf("serving cert not active"))
			continue
		}

		// Check if CA hash matches expected
		caHash := hashFunc(caBundle)
		if hash, ok := secret.GetAnnotations()[OLMCAHashAnnotationKey]; !ok || hash != caHash {
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
		if k8serrors.IsNotFound(err) || err != nil {
			logger.WithField("deployment", desc.DeploymentName).Warnf("expected Deployment could not be retrieved")
			errs = append(errs, err)
			continue
		}
		if hash, ok := deployment.Spec.Template.GetAnnotations()[OLMCAHashAnnotationKey]; !ok || hash != caHash {
			logger.WithField("deployment", desc.DeploymentName).Warnf("Deployment CA cert hash does not match expected")
			errs = append(errs, fmt.Errorf("Deployment %s CA cert hash does not match expected", desc.DeploymentName))
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
			kubeSystem:          {},
			metav1.NamespaceAll: {},
		}

		// extension-apiserver-authentication-reader
		authReaderRole, err := a.lister.RbacV1().RoleLister().Roles(kubeSystem).Get("extension-apiserver-authentication-reader")
		if err != nil {
			logger.Warnf("could not retrieve Role extension-apiserver-authentication-reader")
			errs = append(errs, err)
			continue
		}
		rulesMap[kubeSystem] = append(rulesMap[kubeSystem], authReaderRole.Rules...)

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
					errs = append(errs, fmt.Errorf("Rule %+v not satisfied", rule))
					continue
				}
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (a *Operator) isAPIServiceAvailable(apiService *apiregistrationv1.APIService) bool {
	for _, c := range apiService.Status.Conditions {
		if c.Type == apiregistrationv1.Available && c.Status == apiregistrationv1.ConditionTrue {
			return true
		}
	}
	return false
}

func (a *Operator) areAPIServicesAvailable(csv *v1alpha1.ClusterServiceVersion) (bool, error) {
	for _, desc := range csv.Spec.APIServiceDefinitions.Owned {
		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(desc.GetName())
		if k8serrors.IsNotFound(err) {
			return false, nil
		}

		if err != nil {
			return false, err
		}

		if !a.isAPIServiceAvailable(apiService) {
			return false, nil
		}

		if err := a.isGVKRegistered(desc.Group, desc.Version, desc.Kind); err != nil {
			return false, nil
		}
	}

	return true, nil
}

func apiServiceDescriptionsForDeployment(descs []v1alpha1.APIServiceDescription, deploymentName string) []v1alpha1.APIServiceDescription {
	result := []v1alpha1.APIServiceDescription{}
	for _, desc := range descs {
		if desc.DeploymentName == deploymentName {
			result = append(result, desc)
		}
	}
	return result
}

func (a *Operator) installOwnedAPIServiceRequirements(csv *v1alpha1.ClusterServiceVersion, strategy install.Strategy) (install.Strategy, error) {
	logger := log.WithFields(log.Fields{
		"csv":       csv.GetName(),
		"namespace": csv.GetNamespace(),
	})

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("unsupported InstallStrategy type")
	}

	// Return early if there are no owned APIServices
	if len(csv.Spec.APIServiceDefinitions.Owned) == 0 {
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

	apiDescs := csv.GetOwnedAPIServiceDescriptions()
	for i, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		descs := apiServiceDescriptionsForDeployment(apiDescs, sddSpec.Name)
		if len(descs) == 0 {
			continue
		}

		// Update the deployment for each api service desc
		newDepSpec, err := a.installAPIServiceRequirements(sddSpec.Name, ca, rotateAt, sddSpec.Spec, csv, getServicePorts(descs))
		if err != nil {
			return nil, err
		}

		caPEM, _, err := ca.ToPEM()
		if err != nil {
			logger.Warnf("unable to convert CA certificate to PEM format for Deployment %s", sddSpec.Name)
			return nil, err
		}

		for _, desc := range descs {
			err = a.createOrUpdateAPIService(caPEM, desc, csv)
			if err != nil {
				return nil, err
			}

			// Cleanup legacy resources
			err = a.deleteLegacyAPIServiceResources(csv, desc)
			if err != nil {
				return nil, err
			}
		}
		strategyDetailsDeployment.DeploymentSpecs[i].Spec = *newDepSpec
	}

	// Set CSV cert status
	now := metav1.Now()
	rotateTime := metav1.NewTime(rotateAt)
	csv.Status.CertsLastUpdated = &now
	csv.Status.CertsRotateAt = &rotateTime

	return strategyDetailsDeployment, nil
}

// updateDeploymentSpecsWithApiServiceData transforms an install strategy to include information about apiservices
// it is used in generating hashes for deployment specs to know when something in the spec has changed,
// but duplicates a lot of installAPIServiceRequirements and should be refactored.
func (a *Operator) updateDeploymentSpecsWithApiServiceData(csv *v1alpha1.ClusterServiceVersion, strategy install.Strategy) (install.Strategy, error) {
	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("unsupported InstallStrategy type")
	}

	apiDescs := csv.GetOwnedAPIServiceDescriptions()

	// Return early if there are no owned APIServices
	if len(apiDescs) == 0 {
		return strategyDetailsDeployment, nil
	}

	depSpecs := make(map[string]appsv1.DeploymentSpec)
	for _, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		depSpecs[sddSpec.Name] = sddSpec.Spec
	}

	for _, desc := range apiDescs {
		apiServiceName := desc.GetName()
		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)
		if err != nil {
			return nil, fmt.Errorf("could not retrieve generated APIService: %v", err)
		}

		caBundle := apiService.Spec.CABundle
		caHash := certs.PEMSHA256(caBundle)

		depSpec, ok := depSpecs[desc.DeploymentName]
		if !ok {
			return nil, fmt.Errorf("StrategyDetailsDeployment missing deployment %s for owned APIServices %s", desc.DeploymentName, fmt.Sprintf("%s.%s", desc.Version, desc.Group))
		}

		if depSpec.Template.Spec.ServiceAccountName == "" {
			depSpec.Template.Spec.ServiceAccountName = "default"
		}

		// Update deployment with secret volume mount.
		secret, err := a.lister.CoreV1().SecretLister().Secrets(csv.GetNamespace()).Get(secretName(serviceName(desc.DeploymentName)))
		if err != nil {
			return nil, fmt.Errorf("Unable to get secret %s", secretName(serviceName(desc.DeploymentName)))
		}

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
		depSpec.Template.ObjectMeta.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})
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

func getServicePorts(descs []v1alpha1.APIServiceDescription) []corev1.ServicePort {
	result := []corev1.ServicePort{}
	for _, desc := range descs {
		if !containsServicePort(result, getServicePort(desc)) {
			result = append(result, getServicePort(desc))
		}
	}

	return result
}

func getServicePort(desc v1alpha1.APIServiceDescription) corev1.ServicePort {
	containerPort := 443
	if desc.ContainerPort > 0 {
		containerPort = int(desc.ContainerPort)
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
func (a *Operator) installAPIServiceRequirements(deploymentName string, ca *certs.KeyPair, rotateAt time.Time, depSpec appsv1.DeploymentSpec, csv *v1alpha1.ClusterServiceVersion, ports []corev1.ServicePort) (*appsv1.DeploymentSpec, error) {
	logger := log.WithFields(log.Fields{
		"csv":            csv.GetName(),
		"namespace":      csv.GetNamespace(),
		"deploymentName": deploymentName,
	})

	// Create a service for the deployment
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports:    ports,
			Selector: depSpec.Selector.MatchLabels,
		},
	}
	service.SetName(serviceName(deploymentName))
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
	secret.SetNamespace(csv.GetNamespace())

	// Add olmcahash as a label to the caPEM
	caPEM, _, err := ca.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert CA certificate to PEM format for Service %s", service)
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
				Namespace: csv.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:auth-delegator",
		},
	}
	authDelegatorClusterRoleBinding.SetName(service.GetName() + "-system:auth-delegator")

	existingAuthDelegatorClusterRoleBinding, err := a.lister.RbacV1().ClusterRoleBindingLister().Get(authDelegatorClusterRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain.
		if ownerutil.AdoptableLabels(existingAuthDelegatorClusterRoleBinding.GetLabels(), true, csv) {
			logger.WithFields(log.Fields{"obj": "authDelegatorCRB", "labels": existingAuthDelegatorClusterRoleBinding.GetLabels()}).Debug("adopting")
			if err := ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, csv); err != nil {
				return nil, err
			}
		}

		// Attempt an update.
		if _, err := a.opClient.UpdateClusterRoleBinding(authDelegatorClusterRoleBinding); err != nil {
			logger.Warnf("could not update auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role.
		if err := ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, csv); err != nil {
			return nil, err
		}
		_, err = a.opClient.CreateClusterRoleBinding(authDelegatorClusterRoleBinding)
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
				Namespace: csv.GetNamespace(),
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

	existingAuthReaderRoleBinding, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(kubeSystem).Get(authReaderRoleBinding.GetName())
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain.
		if ownerutil.AdoptableLabels(existingAuthReaderRoleBinding.GetLabels(), true, csv) {
			logger.WithFields(log.Fields{"obj": "existingAuthReaderRB", "labels": existingAuthReaderRoleBinding.GetLabels()}).Debug("adopting")
			if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, csv); err != nil {
				return nil, err
			}
		}
		// Attempt an update.
		if _, err := a.opClient.UpdateRoleBinding(authReaderRoleBinding); err != nil {
			logger.Warnf("could not update auth reader role binding %s", authReaderRoleBinding.GetName())
			return nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role.
		if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, csv); err != nil {
			return nil, err
		}
		_, err = a.opClient.CreateRoleBinding(authReaderRoleBinding)
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

func (a *Operator) createOrUpdateAPIService(caPEM []byte, desc v1alpha1.APIServiceDescription, csv *v1alpha1.ClusterServiceVersion) error {
	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
	logger := log.WithFields(log.Fields{
		"csv":        csv.GetName(),
		"namespace":  csv.GetNamespace(),
		"apiservice": fmt.Sprintf("%s.%s", desc.Version, desc.Group),
	})

	exists := true
	apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)
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
		adoptable, err := a.isAPIServiceAdoptable(csv, apiService)
		if err != nil {
			logger.WithFields(log.Fields{"obj": "apiService", "labels": apiService.GetLabels()}).Errorf("adoption check failed - %v", err)
		}

		if !adoptable {
			return fmt.Errorf("pre-existing APIService %s.%s is not adoptable", desc.Version, desc.Group)
		}
	}

	// Add the CSV as an owner
	if err := ownerutil.AddOwnerLabels(apiService, csv); err != nil {
		return err
	}

	// Create a service for the deployment
	containerPort := int32(443)
	if desc.ContainerPort > 0 {
		containerPort = desc.ContainerPort
	}
	// update the ServiceReference
	apiService.Spec.Service = &apiregistrationv1.ServiceReference{
		Namespace: csv.GetNamespace(),
		Name:      serviceName(desc.DeploymentName),
		Port:      &containerPort,
	}

	// create a fresh CA bundle
	apiService.Spec.CABundle = caPEM

	// attempt a update or create
	if exists {
		logger.Debug("updating APIService")
		_, err = a.opClient.UpdateAPIService(apiService)
	} else {
		logger.Debug("creating APIService")
		_, err = a.opClient.CreateAPIService(apiService)
	}

	if err != nil {
		logger.Warnf("could not create or update APIService")
		return err
	}

	return nil
}

func (a *Operator) isAPIServiceAdoptable(target *v1alpha1.ClusterServiceVersion, apiService *apiregistrationv1.APIService) (adoptable bool, err error) {
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
		a.logger.Warn(err.Error())
	}

	targetKind := target.GetObjectKind().GroupVersionKind().Kind
	if ownerKind != targetKind {
		return
	}

	// Get the CSV that target replaces
	replacing, replaceGetErr := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(target.GetNamespace()).Get(target.Spec.Replaces)
	if replaceGetErr != nil && !k8serrors.IsNotFound(replaceGetErr) && !k8serrors.IsGone(replaceGetErr) {
		err = replaceGetErr
		return
	}

	// Get the current owner CSV of the APIService
	currentOwnerCSV, ownerGetErr := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ownerNamespace).Get(ownerName)
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

// deleteLegacyAPIServiceResources deletes resources that were created by OLM for an APIService that used the old naming convention.
func (a *Operator) deleteLegacyAPIServiceResources(owner ownerutil.Owner, desc v1alpha1.APIServiceDescription) error {
	logger := log.WithFields(log.Fields{
		"ownerName":      owner.GetName(),
		"ownerNamespace": owner.GetNamespace(),
		"ownerKind":      owner.GetObjectKind().GroupVersionKind().GroupKind().Kind,
	})
	namespace := owner.GetNamespace()
	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)

	// Handle an edgecase where the legacy resources names matches the new names.
	// This only occurs when the Group of the description matches the name of the deployment
	// and the version is equal to "service".
	if legacyAPIServiceNameToServiceName(apiServiceName) == serviceName(desc.DeploymentName) {
		return nil
	}

	// Handle an edgecase where the legacy resources names matches the new names.
	// This only occurs when the version of the description matches the name of the deployment
	// and the group is equal to "service"
	// If the names match, do not delete the service as OLM has already updated it.
	legacyServiceName := legacyAPIServiceNameToServiceName(apiServiceName)
	if legacyServiceName != serviceName(desc.DeploymentName) {
		// Attempt to delete the legacy Service.
		existingService, err := a.opClient.GetService(namespace, legacyServiceName)
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return err
			}
		} else if ownerutil.AdoptableLabels(existingService.GetLabels(), true, owner) {
			logger.Infof("Deleting Service with legacy APIService name %s", existingService.Name)
			err = a.opClient.DeleteService(namespace, legacyServiceName, &metav1.DeleteOptions{})
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
	existingSecret, err := a.opClient.GetSecret(namespace, secretName(apiServiceName))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingSecret.GetLabels(), true, owner) {
		logger.Infof("Deleting Secret with legacy APIService name %s", existingSecret.Name)
		err = a.opClient.DeleteSecret(namespace, secretName(apiServiceName), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("Secret with legacy APIService  %s not adoptable", existingSecret.Name)
	}

	// Attempt to delete the legacy Role.
	existingRole, err := a.opClient.GetRole(namespace, secretName(apiServiceName))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingRole.GetLabels(), true, owner) {
		logger.Infof("Deleting Role with legacy APIService name %s", existingRole.Name)
		err = a.opClient.DeleteRole(namespace, secretName(apiServiceName), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("Role with legacy APIService name %s not adoptable", existingRole.Name)
	}

	// Attempt to delete the legacy secret RoleBinding.
	existingRoleBinding, err := a.opClient.GetRoleBinding(namespace, secretName(apiServiceName))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingRoleBinding.GetLabels(), true, owner) {
		logger.Infof("Deleting RoleBinding with legacy APIService name %s", existingRoleBinding.Name)
		err = a.opClient.DeleteRoleBinding(namespace, secretName(apiServiceName), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("RoleBinding with legacy APIService name %s not adoptable", existingRoleBinding.Name)
	}

	// Attempt to delete the legacy ClusterRoleBinding.
	existingClusterRoleBinding, err := a.opClient.GetClusterRoleBinding(apiServiceName + "-system:auth-delegator")
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingClusterRoleBinding.GetLabels(), true, owner) {
		logger.Infof("Deleting ClusterRoleBinding with legacy APIService name %s", existingClusterRoleBinding.Name)
		err = a.opClient.DeleteClusterRoleBinding(apiServiceName+"-system:auth-delegator", &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("ClusterRoleBinding with legacy APIService name %s not adoptable", existingClusterRoleBinding.Name)
	}

	// Attempt to delete the legacy AuthReadingRoleBinding.
	existingRoleBinding, err = a.opClient.GetRoleBinding(kubeSystem, apiServiceName+"-auth-reader")
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingRoleBinding.GetLabels(), true, owner) {
		logger.Infof("Deleting RoleBinding with legacy APIService name %s", existingRoleBinding.Name)
		err = a.opClient.DeleteRoleBinding(kubeSystem, apiServiceName+"-auth-reader", &metav1.DeleteOptions{})
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
