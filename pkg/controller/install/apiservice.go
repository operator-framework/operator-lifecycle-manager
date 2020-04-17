package install

import (
	"errors"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

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
		Name:      ServiceName(desc.DeploymentName),
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
func (i *StrategyDeploymentInstaller) deleteLegacyAPIServiceResources(desc apiServiceDescriptionsWithCAPEM) error {
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
	if legacyAPIServiceNameToServiceName(apiServiceName) == ServiceName(desc.apiServiceDescription.DeploymentName) {
		return nil
	}

	// Handle an edgecase where the legacy resources names matches the new names.
	// This only occurs when the version of the description matches the name of the deployment
	// and the group is equal to "service"
	// If the names match, do not delete the service as OLM has already updated it.
	legacyServiceName := legacyAPIServiceNameToServiceName(apiServiceName)
	if legacyServiceName != ServiceName(desc.apiServiceDescription.DeploymentName) {
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
	existingSecret, err := i.strategyClient.GetOpClient().GetSecret(namespace, SecretName(apiServiceName))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingSecret.GetLabels(), true, i.owner) {
		logger.Infof("Deleting Secret with legacy APIService name %s", existingSecret.Name)
		err = i.strategyClient.GetOpClient().DeleteSecret(namespace, SecretName(apiServiceName), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("Secret with legacy APIService  %s not adoptable", existingSecret.Name)
	}

	// Attempt to delete the legacy Role.
	existingRole, err := i.strategyClient.GetOpClient().GetRole(namespace, SecretName(apiServiceName))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingRole.GetLabels(), true, i.owner) {
		logger.Infof("Deleting Role with legacy APIService name %s", existingRole.Name)
		err = i.strategyClient.GetOpClient().DeleteRole(namespace, SecretName(apiServiceName), &metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else {
		logger.Infof("Role with legacy APIService name %s not adoptable", existingRole.Name)
	}

	// Attempt to delete the legacy secret RoleBinding.
	existingRoleBinding, err := i.strategyClient.GetOpClient().GetRoleBinding(namespace, SecretName(apiServiceName))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingRoleBinding.GetLabels(), true, i.owner) {
		logger.Infof("Deleting RoleBinding with legacy APIService name %s", existingRoleBinding.Name)
		err = i.strategyClient.GetOpClient().DeleteRoleBinding(namespace, SecretName(apiServiceName), &metav1.DeleteOptions{})
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
	existingRoleBinding, err = i.strategyClient.GetOpClient().GetRoleBinding(KubeSystem, apiServiceName+"-auth-reader")
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
	} else if ownerutil.AdoptableLabels(existingRoleBinding.GetLabels(), true, i.owner) {
		logger.Infof("Deleting RoleBinding with legacy APIService name %s", existingRoleBinding.Name)
		err = i.strategyClient.GetOpClient().DeleteRoleBinding(KubeSystem, apiServiceName+"-auth-reader", &metav1.DeleteOptions{})
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
