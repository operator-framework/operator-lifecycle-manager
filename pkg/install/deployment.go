package install

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	rbac "k8s.io/api/rbac/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

// StrategyDeploymentSpec contains the name and spec for the deployment ALM should create
type StrategyDeploymentSpec struct {
	Name string                 `json:"name"`
	Spec v1beta1.DeploymentSpec `json:"spec"`
}

// StrategyDetailsDeployment represents the parsed details of a Deployment
// InstallStrategy.
type StrategyDetailsDeployment struct {
	DeploymentSpecs []StrategyDeploymentSpec        `json:"deployments"`
	Permissions     []StrategyDeploymentPermissions `json:"permissions"`
}

type StrategyDeploymentInstaller struct {
	strategyClient   client.InstallStrategyDeploymentInterface
	ownerRefs        []metav1.OwnerReference
	ownerMeta        metav1.ObjectMeta
	previousStrategy Strategy
}

func (d *StrategyDetailsDeployment) GetStrategyName() string {
	return InstallStrategyNameDeployment
}

var _ Strategy = &StrategyDetailsDeployment{}
var _ StrategyInstaller = &StrategyDeploymentInstaller{}

func NewStrategyDeploymentInstaller(strategyClient client.InstallStrategyDeploymentInterface, ownerMeta metav1.ObjectMeta, previousStrategy Strategy) StrategyInstaller {
	return &StrategyDeploymentInstaller{
		strategyClient: strategyClient,
		ownerRefs: []metav1.OwnerReference{
			{
				APIVersion:         v1alpha1.SchemeGroupVersion.String(),
				Kind:               v1alpha1.ClusterServiceVersionKind,
				Name:               ownerMeta.GetName(),
				UID:                ownerMeta.UID,
				Controller:         &Controller,
				BlockOwnerDeletion: &BlockOwnerDeletion,
			},
		},
		ownerMeta:        ownerMeta,
		previousStrategy: previousStrategy,
	}
}

func (i *StrategyDeploymentInstaller) installPermissions(perms []StrategyDeploymentPermissions) error {
	for _, permission := range perms {
		// create role
		role := &rbac.Role{
			Rules: permission.Rules,
		}
		role.SetOwnerReferences(i.ownerRefs)
		role.SetGenerateName(fmt.Sprintf("%s-role-", i.ownerMeta.GetName()))
		createdRole, err := i.strategyClient.CreateRole(role)
		if err != nil {
			return err
		}

		// create serviceaccount if necessary
		serviceAccount := &corev1.ServiceAccount{}
		serviceAccount.SetOwnerReferences(i.ownerRefs)
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
		roleBinding.SetOwnerReferences(i.ownerRefs)
		roleBinding.SetGenerateName(fmt.Sprintf("%s-%s-rolebinding-", createdRole.Name, serviceAccount.Name))

		if _, err := i.strategyClient.CreateRoleBinding(roleBinding); err != nil {
			return err
		}
	}
	return nil
}

func (i *StrategyDeploymentInstaller) installDeployments(deps []StrategyDeploymentSpec) error {
	for _, d := range deps {
		// Create or Update Deployment
		dep := v1beta1.Deployment{Spec: d.Spec}
		dep.SetName(d.Name)
		dep.SetNamespace(i.ownerMeta.Namespace)
		dep.SetOwnerReferences(i.ownerRefs)
		if dep.Labels == nil {
			dep.SetLabels(map[string]string{})
		}
		dep.Labels["alm-owner-name"] = i.ownerMeta.Name
		dep.Labels["alm-owner-namespace"] = i.ownerMeta.Namespace
		if _, err := i.strategyClient.CreateOrUpdateDeployment(&dep); err != nil {
			return err
		}
	}

	return nil
}

func (i *StrategyDeploymentInstaller) cleanupPrevious(current *StrategyDetailsDeployment, previous *StrategyDetailsDeployment) error {
	previousDeploymentsMap := map[string]struct{}{}
	for _, d := range previous.DeploymentSpecs {
		previousDeploymentsMap[d.Name] = struct{}{}
	}
	for _, d := range current.DeploymentSpecs {
		delete(previousDeploymentsMap, d.Name)
	}
	log.Debugf("preparing to cleanup: %s", previousDeploymentsMap)
	// delete deployments in old strategy but not new
	var err error = nil
	for name := range previousDeploymentsMap {
		err = i.strategyClient.DeleteDeployment(name)
	}
	return err
}

func (i *StrategyDeploymentInstaller) Install(s Strategy) error {
	strategy, ok := s.(*StrategyDetailsDeployment)
	if !ok {
		return fmt.Errorf("attempted to install %s strategy with deployment installer", strategy.GetStrategyName())
	}

	if err := i.installPermissions(strategy.Permissions); err != nil {
		return err
	}

	if err := i.installDeployments(strategy.DeploymentSpecs); err != nil {
		return err
	}

	if i.previousStrategy != nil {
		previous, ok := i.previousStrategy.(*StrategyDetailsDeployment)
		if !ok {
			return fmt.Errorf("couldn't parse old install %s strategy with deployment installer", previous.GetStrategyName())
		}
		return i.cleanupPrevious(strategy, previous)
	}
	return nil
}

// CheckInstalled can return nil (installed), or errors
// Errors can indicate: some component missing (keep installing), unable to query (check again later), or unrecoverable (failed in a way we know we can't recover from)
func (i *StrategyDeploymentInstaller) CheckInstalled(s Strategy) (installed bool, err error) {
	strategy, ok := s.(*StrategyDetailsDeployment)
	if !ok {
		return false, StrategyError{Reason: StrategyErrReasonInvalidStrategy, Message: fmt.Sprintf("attempted to check %s strategy with deployment installer", strategy.GetStrategyName())}
	}

	// Check service accounts
	for _, perm := range strategy.Permissions {
		if err := i.checkForServiceAccount(perm.ServiceAccountName); err != nil {
			return false, err
		}
	}

	// Check deployments
	if err := i.checkForDeployments(strategy.DeploymentSpecs); err != nil {
		return false, err
	}
	return true, nil
}

func (i *StrategyDeploymentInstaller) checkForServiceAccount(serviceAccountName string) error {
	if _, err := i.strategyClient.GetServiceAccountByName(serviceAccountName); err != nil {
		if apierrors.IsNotFound(err) {
			log.Debugf("service account not found: %s", serviceAccountName)
			return StrategyError{Reason: StrategyErrReasonComponentMissing, Message: fmt.Sprintf("service account not found: %s", serviceAccountName)}
		}
		log.Debugf("error querying for %s: %s", serviceAccountName, err)
		return StrategyError{Reason: StrategyErrReasonComponentMissing, Message: fmt.Sprintf("error querying for %s: %s", serviceAccountName, err)}
	}
	// TODO: use a SelfSubjectRulesReview (or a sync version) to verify ServiceAccount has correct access
	return nil
}

func (i *StrategyDeploymentInstaller) checkForDeployments(deploymentSpecs []StrategyDeploymentSpec) error {
	var depNames []string
	for _, dep := range deploymentSpecs {
		depNames = append(depNames, dep.Name)
	}

	existingDeployments, err := i.strategyClient.FindAnyDeploymentsMatchingNames(depNames)
	if err != nil {
		return StrategyError{Reason: StrategyErrReasonComponentMissing, Message: fmt.Sprintf("error querying for %s: %s", depNames, err)}
	}

	// compare deployments to see if any need to be created/updated
	existingMap := map[string]*v1beta1.Deployment{}
	for _, d := range existingDeployments {
		existingMap[d.GetName()] = d
	}
	for _, spec := range deploymentSpecs {
		dep, exists := existingMap[spec.Name]
		if !exists {
			log.Debugf("missing deployment with name=%s", spec.Name)
			return StrategyError{Reason: StrategyErrReasonComponentMissing, Message: fmt.Sprintf("missing deployment with name=%s", spec.Name)}
		}
		reason, ready, err := DeploymentStatus(dep)
		if err != nil {
			log.Debugf("deployment %s not ready before timeout: %s", dep.Name, err.Error())
			return StrategyError{Reason: StrategyErrReasonTimeout, Message: fmt.Sprintf("deployment %s not ready before timeout: %s", dep.Name, err.Error())}
		}
		if !ready {
			return StrategyError{Reason: StrategyErrReasonWaiting, Message: fmt.Sprintf("waiting for deployment %s to become ready: %s", dep.Name, reason)}
		}
	}
	return nil
}
