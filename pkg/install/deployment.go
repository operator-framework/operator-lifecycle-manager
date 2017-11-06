package install

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	rbac "k8s.io/api/rbac/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/pkg/apis"
	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/client"
)

const (
	InstallStrategyNameDeployment = "deployment"
)

var BlockOwnerDeletion = true
var Controller = false

// StrategyDeploymentPermissions describe the rbac rules and service account needed by the install strategy
type StrategyDeploymentPermissions struct {
	ServiceAccountName string            `json:"serviceAccountName"`
	Rules              []rbac.PolicyRule `json:"rules"`
}

// StrategyDetailsDeployment represents the parsed details of a Deployment
// InstallStrategy.
type StrategyDetailsDeployment struct {
	DeploymentSpecs []v1beta1.DeploymentSpec        `json:"deployments"`
	Permissions     []StrategyDeploymentPermissions `json:"permissions"`
}

type StrategyDeploymentInstaller struct {
	strategyClient client.InstallStrategyDeploymentInterface
	ownerMeta      metav1.ObjectMeta
}

func (d *StrategyDetailsDeployment) GetStrategyName() string {
	return InstallStrategyNameDeployment
}

var _ Strategy = &StrategyDetailsDeployment{}
var _ StrategyInstaller = &StrategyDeploymentInstaller{}

func NewStrategyDeploymentInstaller(strategyClient client.InstallStrategyDeploymentInterface, ownerMeta metav1.ObjectMeta) StrategyInstaller {
	return &StrategyDeploymentInstaller{
		strategyClient: strategyClient,
		ownerMeta:      ownerMeta,
	}
}

func (i *StrategyDeploymentInstaller) Install(s Strategy) error {
	strategy, ok := s.(*StrategyDetailsDeployment)
	if !ok {
		return fmt.Errorf("attempted to install %s strategy with deployment installer", strategy.GetStrategyName())
	}
	ownerReferences := []metav1.OwnerReference{
		{
			APIVersion:         apis.GroupName,
			Kind:               v1alpha1.ClusterServiceVersionKind,
			Name:               i.ownerMeta.GetName(),
			UID:                i.ownerMeta.UID,
			Controller:         &Controller,
			BlockOwnerDeletion: &BlockOwnerDeletion,
		},
	}
	for _, permission := range strategy.Permissions {
		// create role
		role := &rbac.Role{
			Rules: permission.Rules,
		}
		role.SetOwnerReferences(ownerReferences)
		role.SetGenerateName(fmt.Sprintf("%s-role-", i.ownerMeta.Name))
		createdRole, err := i.strategyClient.CreateRole(role)
		if err != nil {
			return err
		}

		// create serviceaccount if necessary
		serviceAccount := &corev1.ServiceAccount{}
		serviceAccount.SetOwnerReferences(ownerReferences)
		serviceAccount.SetName(permission.ServiceAccountName)
		serviceAccount, err = i.strategyClient.EnsureServiceAccount(serviceAccount)
		if err != nil {
			return err
		}

		// create rolebinding
		roleBinding := &rbac.RoleBinding{
			RoleRef: rbac.RoleRef{
				Kind:     "Role",
				Name:     createdRole.GetName(),
				APIGroup: rbac.GroupName},
			Subjects: []rbac.Subject{{
				Kind:      "ServiceAccount",
				Name:      permission.ServiceAccountName,
				Namespace: i.ownerMeta.Namespace,
			}},
		}
		roleBinding.SetOwnerReferences(ownerReferences)
		roleBinding.SetGenerateName(fmt.Sprintf("%s-%s-rolebinding-", createdRole.Name, serviceAccount.Name))

		if _, err = i.strategyClient.CreateRoleBinding(roleBinding); err != nil {
			return err
		}
	}

	for _, spec := range strategy.DeploymentSpecs {
		dep := v1beta1.Deployment{Spec: spec}
		dep.SetNamespace(i.ownerMeta.Namespace)
		dep.SetOwnerReferences(ownerReferences)
		dep.SetGenerateName(fmt.Sprintf("%s-", i.ownerMeta.Name))
		if dep.Labels == nil {
			dep.SetLabels(map[string]string{})
		}
		dep.Labels["alm-owner-name"] = i.ownerMeta.Name
		dep.Labels["alm-owner-namespace"] = i.ownerMeta.Namespace
		if _, err := i.strategyClient.CreateDeployment(&dep); err != nil {
			return err
		}
	}
	return nil
}

func (i *StrategyDeploymentInstaller) CheckInstalled(s Strategy) (bool, error) {
	strategy, ok := s.(*StrategyDetailsDeployment)
	if !ok {
		return false, fmt.Errorf("attempted to check %s strategy with deployment installer", strategy.GetStrategyName())
	}

	// Check service accounts
	for _, perm := range strategy.Permissions {
		if found, err := i.checkForServiceAccount(perm.ServiceAccountName); !found {
			log.Debugf("service account not found: %s", perm.ServiceAccountName)
			return false, err
		}
	}

	// Check deployments
	if found, err := i.checkForOwnedDeployments(i.ownerMeta, strategy.DeploymentSpecs); !found {
		log.Debug("deployments not found")
		return false, err
	}
	return true, nil
}

func (i *StrategyDeploymentInstaller) checkForServiceAccount(serviceAccountName string) (bool, error) {
	if _, err := i.strategyClient.GetServiceAccountByName(serviceAccountName); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("query for service account %s failed: %s", serviceAccountName, err.Error())
	}
	// TODO: use a SelfSubjectRulesReview (or a sync version) to verify ServiceAccount has correct access
	return true, nil
}

func (i *StrategyDeploymentInstaller) checkForOwnedDeployments(owner metav1.ObjectMeta, deploymentSpecs []v1beta1.DeploymentSpec) (bool, error) {
	existingDeployments, err := i.strategyClient.GetOwnedDeployments(owner)
	if err != nil {
		return false, fmt.Errorf("query for existing deployments failed: %s", err)
	}
	if len(existingDeployments.Items) != len(deploymentSpecs) {
		log.Debugf("wrong number of deployments found. want %d, got %d", len(deploymentSpecs), len(existingDeployments.Items))
		return false, nil
	}
	return true, nil
}
