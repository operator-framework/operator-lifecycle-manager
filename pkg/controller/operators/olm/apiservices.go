package olm

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apiserver/pkg/storage/names"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func (a *Operator) syncAPIServices(obj interface{}) (syncError error) {
	apiService, ok := obj.(*apiregistrationv1.APIService)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting APIService failed")
	}
	if ownerutil.IsOwnedByKind(apiService, v1alpha1.ClusterServiceVersionKind) {
		oref := ownerutil.GetOwnerByKind(apiService, v1alpha1.ClusterServiceVersionKind)
		log.Infof("APIService %s change requeuing CSV %s", apiService.GetName(), oref.Name)
		a.requeueCSV(oref.Name, apiService.Spec.Service.Namespace)
	}

	return nil
}

func (a *Operator) isAPIServiceAvailable(apiService *apiregistrationv1.APIService) bool {
	for _, c := range apiService.Status.Conditions {
		if c.Type == apiregistrationv1.Available && c.Status == apiregistrationv1.ConditionTrue {
			return true
		}
	}
	return false
}

func (a *Operator) areAPIServicesAvailable(descs []v1alpha1.APIServiceDescription) (bool, error) {
	for _, desc := range descs {
		apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
		apiService, err := a.OpClient.GetAPIService(apiServiceName)
		if err != nil {
			return false, err
		}

		if !a.isAPIServiceAvailable(apiService) {
			return false, fmt.Errorf("APIService %s not available", apiService.GetName())
		}

		if err := a.isGVKRegistered(desc.Group, desc.Version, desc.Kind); err != nil {
			return false, fmt.Errorf("group: %s, version: %s, kind: %s not registered", desc.Group, desc.Version, desc.Kind)
		}
	}

	return true, nil
}

func (a *Operator) installOwnedAPIServiceRequirements(csv *v1alpha1.ClusterServiceVersion, strategy install.Strategy) (install.Strategy, error) {
	logger := log.WithFields(log.Fields{
		"csv":       csv.GetName(),
		"namespace": csv.GetNamespace(),
	})

	// generate ca
	ca, err := certs.GenerateCA()
	if err != nil {
		logger.Debug("failed to generate CA")
		return nil, err
	}

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*install.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("unsupported InstallStrategy type")
	}

	depSpecs := make(map[string]appsv1.DeploymentSpec)
	for _, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		depSpecs[sddSpec.Name] = sddSpec.Spec
	}

	// TODO(Nick): return more descriptive errors and return individual status conditions
	// for all owned APIServiceDescriptions, create all resources required, and update
	// the matching DeploymentSpec's Volume and VolumeMounts
	apiDescs := csv.GetOwnedAPIServiceDescriptions()
	for _, desc := range apiDescs {
		depSpec, ok := depSpecs[desc.DeploymentName]
		if !ok {
			return nil, fmt.Errorf("StrategyDetailsDeployment missing deployment %s for owned APIService %s", desc.DeploymentName, fmt.Sprintf("%s.%s", desc.Version, desc.Group))
		}
		newDepSpec, err := a.installAPIServiceRequirements(desc, ca, depSpec, csv)
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

	return strategyDetailsDeployment, nil
}

func (a *Operator) installAPIServiceRequirements(desc v1alpha1.APIServiceDescription, ca *certs.KeyPair, depSpec appsv1.DeploymentSpec, csv *v1alpha1.ClusterServiceVersion) (*appsv1.DeploymentSpec, error) {
	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
	logger := log.WithFields(log.Fields{
		"csv":        csv.GetName(),
		"namespace":  csv.GetNamespace(),
		"apiservice": apiServiceName,
	})

	// create a service for the deployment
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

	// TODO(Nick): ensure service name is a valid DNS name
	// replace all '.'s with "-"s to convert to a DNS-1035 label
	service.SetName(strings.Replace(apiServiceName, ".", "-", -1))
	service.SetNamespace(csv.GetNamespace())
	ownerutil.AddNonBlockingOwner(service, csv)

	_, err := a.OpClient.CreateService(service)
	if k8serrors.IsAlreadyExists(err) {
		// attempt a replace
		deleteErr := a.OpClient.DeleteService(service.GetNamespace(), service.GetName(), &metav1.DeleteOptions{})
		if _, err := a.OpClient.CreateService(service); err != nil || deleteErr != nil {
			logger.Debugf("could not replace service %s", service.GetName())
			return nil, err
		}
	} else if err != nil {
		log.Debugf("could not create service %s", service.GetName())
		return nil, err
	}

	// create signed serving cert
	hosts := []string{
		fmt.Sprintf("%s.%s", service.GetName(), csv.GetNamespace()),
		fmt.Sprintf("%s.%s.svc", service.GetName(), csv.GetNamespace()),
	}
	servingPair, err := certs.CreateSignedServingPair(ca, hosts)
	if err != nil {
		log.Printf("could not generate signed certs for hosts %v", hosts)
		return nil, err
	}

	// create Secret for serving cert
	certPEM, privPEM, err := servingPair.ToPEM()
	if err != nil {
		logger.Debugf("unable to convert serving certificate and private key to PEM format for APIService %s", apiServiceName)
		return nil, err
	}

	secret := &corev1.Secret{
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": privPEM,
		},
		Type: corev1.SecretTypeTLS,
	}
	secret.SetName(apiServiceName + "-cert")
	secret.SetNamespace(csv.GetNamespace())
	ownerutil.AddNonBlockingOwner(secret, csv)

	_, err = a.OpClient.CreateSecret(secret)
	if k8serrors.IsAlreadyExists(err) {
		// attempt an update
		if _, err := a.OpClient.UpdateSecret(secret); err != nil {
			logger.Debugf("could not update Secret %s", secret.GetName())
			return nil, err
		}
	} else if err != nil {
		log.Debugf("could not create Secret %s", secret.GetName())
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
	ownerutil.AddNonBlockingOwner(secretRole, csv)

	_, err = a.OpClient.CreateRole(secretRole)
	if k8serrors.IsAlreadyExists(err) {
		// attempt an update
		if _, err := a.OpClient.UpdateRole(secretRole); err != nil {
			logger.Debugf("could not update Role %s", secretRole.GetName())
			return nil, err
		}
	} else if err != nil {
		log.Debugf("could not create Role %s", secretRole.GetName())
		return nil, err
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
	ownerutil.AddNonBlockingOwner(secretRoleBinding, csv)

	_, err = a.OpClient.CreateRoleBinding(secretRoleBinding)
	if k8serrors.IsAlreadyExists(err) {
		// attempt an update
		if _, err := a.OpClient.UpdateRoleBinding(secretRoleBinding); err != nil {
			logger.Debugf("could not update RoleBinding %s", secretRoleBinding.GetName())
			return nil, err
		}
	} else if err != nil {
		log.Debugf("could not create RoleBinding %s", secretRoleBinding.GetName())
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
	authDelegatorClusterRoleBinding.SetName(apiServiceName + "-system:auth-delegator")
	ownerutil.AddNonBlockingOwner(authDelegatorClusterRoleBinding, csv)

	_, err = a.OpClient.CreateClusterRoleBinding(authDelegatorClusterRoleBinding)
	if k8serrors.IsAlreadyExists(err) {
		// attempt an update
		if _, err := a.OpClient.UpdateClusterRoleBinding(authDelegatorClusterRoleBinding); err != nil {
			logger.Debugf("could not update ClusterRoleBinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, err
		}
	} else if err != nil {
		log.Debugf("could not create ClusterRoleBinding %s", authDelegatorClusterRoleBinding.GetName())
		return nil, err
	}

	// create RoleBinding to extension-apiserver-authentication-reader Role in the kube-system namespace
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
	authReaderRoleBinding.SetName(apiServiceName + "-auth-reader")
	authReaderRoleBinding.SetNamespace("kube-system")
	ownerutil.AddNonBlockingOwner(authReaderRoleBinding, csv)

	_, err = a.OpClient.CreateRoleBinding(authReaderRoleBinding)
	if k8serrors.IsAlreadyExists(err) {
		// attempt an update
		if _, err := a.OpClient.UpdateRoleBinding(authReaderRoleBinding); err != nil {
			logger.Debugf("could not update RoleBinding %s", authReaderRoleBinding.GetName())
			return nil, err
		}
	} else if err != nil {
		log.Debugf("could not create RoleBinding %s", authReaderRoleBinding.GetName())
		return nil, err
	}

	// update deployment with secret volume mount
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

	// TODO(NICK): limit which containers get a volume mount
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

			// replace if mounting to the same location
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

	// changing depSpec.Template's name to force a rollout
	// ensures that the new secret is used by the apiserver if not hot reloading
	podTemplateName := names.SimpleNameGenerator.GenerateName(apiServiceName)
	if podTemplateName == depSpec.Template.GetName() {
		return nil, fmt.Errorf("a name collision occured when generating name for PodTemplate")
	}
	depSpec.Template.SetName(podTemplateName)

	exists := true
	apiService, err := a.OpClient.GetAPIService(apiServiceName)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return nil, err
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
		// check if the APIService is adoptable
		if !ownerutil.Adoptable(csv, apiService.GetOwnerReferences()) {
			return nil, fmt.Errorf("pre-existing APIService %s is not adoptable", apiServiceName)
		}
	}

	// Add the CSV as an owner
	ownerutil.AddNonBlockingOwner(apiService, csv)

	// update the ServiceReference
	apiService.Spec.Service = &apiregistrationv1.ServiceReference{
		Namespace: service.GetNamespace(),
		Name:      service.GetName(),
	}

	// create a fresh CA bundle
	caCertPEM, _, err := ca.ToPEM()
	if err != nil {
		logger.Debugf("unable to convert CA certificate to PEM format for APIService %s", apiServiceName)
		return nil, err
	}
	apiService.Spec.CABundle = caCertPEM

	// attempt a update or create
	if exists {
		_, err = a.OpClient.UpdateAPIService(apiService)

	} else {
		_, err = a.OpClient.CreateAPIService(apiService)
	}

	if err != nil {
		log.Debugf("could not create or update APIService %s", apiServiceName)
		return nil, err
	}

	return &depSpec, nil
}
