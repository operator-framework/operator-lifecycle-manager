package olm

import (
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	olmErrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (a *Operator) requirementStatus(strategyDetailsDeployment *install.StrategyDetailsDeployment, crdDescs []v1alpha1.CRDDescription,
	ownedAPIServiceDescs []v1alpha1.APIServiceDescription, requiredAPIServiceDescs []v1alpha1.APIServiceDescription,
	requiredNativeAPIs []metav1.GroupVersionKind) (met bool, statuses []v1alpha1.RequirementStatus) {
	met = true

	// Check for CRDs
	for _, r := range crdDescs {
		status := v1alpha1.RequirementStatus{
			Group:   "apiextensions.k8s.io",
			Version: "v1beta1",
			Kind:    "CustomResourceDefinition",
			Name:    r.Name,
		}

		// check if CRD exists - this verifies group, version, and kind, so no need for GVK check via discovery
		crd, err := a.lister.APIExtensionsV1beta1().CustomResourceDefinitionLister().Get(r.Name)
		if err != nil {
			a.Log.Debugf("Setting 'met' to false, %v with err: %v", r.Name, err)
			status.Status = v1alpha1.RequirementStatusReasonNotPresent
			met = false
			statuses = append(statuses, status)
			continue
		}

		if crd.Spec.Version == r.Version {
			status.Status = v1alpha1.RequirementStatusReasonPresent
		} else {
			served := false
			for _, version := range crd.Spec.Versions {
				if version.Name == r.Version {
					if version.Served {
						status.Status = v1alpha1.RequirementStatusReasonPresent
						served = true
					}
					break
				}
			}

			if !served {
				status.Status = v1alpha1.RequirementStatusReasonNotPresent
				met = false
				statuses = append(statuses, status)
				continue
			}
		}

		// Check if CRD has successfully registered with k8s API
		established := false
		namesAccepted := false
		for _, cdt := range crd.Status.Conditions {
			switch cdt.Type {
			case v1beta1.Established:
				if cdt.Status == v1beta1.ConditionTrue {
					established = true
				}
			case v1beta1.NamesAccepted:
				if cdt.Status == v1beta1.ConditionTrue {
					namesAccepted = true
				}
			}
		}

		if established && namesAccepted {
			status.UUID = string(crd.GetUID())
			statuses = append(statuses, status)
		} else {
			status.Status = v1alpha1.RequirementStatusReasonNotAvailable
			met = false
			statuses = append(statuses, status)
		}
	}

	// Check for required API services
	for _, r := range requiredAPIServiceDescs {
		name := fmt.Sprintf("%s.%s", r.Version, r.Group)
		status := v1alpha1.RequirementStatus{
			Group:   "apiregistration.k8s.io",
			Version: "v1",
			Kind:    "APIService",
			Name:    name,
		}

		// Check if GVK exists
		if err := a.isGVKRegistered(r.Group, r.Version, r.Kind); err != nil {
			status.Status = "NotPresent"
			met = false
			statuses = append(statuses, status)
			continue
		}

		// Check if APIService is registered
		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(name)
		if err != nil {
			status.Status = "NotPresent"
			met = false
			statuses = append(statuses, status)
			continue
		}

		// Check if API is available
		if !a.isAPIServiceAvailable(apiService) {
			status.Status = "NotPresent"
			met = false
		} else {
			status.Status = "Present"
			status.UUID = string(apiService.GetUID())
		}
		statuses = append(statuses, status)
	}

	// Check owned API services
	for _, r := range ownedAPIServiceDescs {
		name := fmt.Sprintf("%s.%s", r.Version, r.Group)
		status := v1alpha1.RequirementStatus{
			Group:   "apiregistration.k8s.io",
			Version: "v1",
			Kind:    "APIService",
			Name:    name,
		}

		found := false
		for _, spec := range strategyDetailsDeployment.DeploymentSpecs {
			if spec.Name == r.DeploymentName {
				status.Status = "DeploymentFound"
				statuses = append(statuses, status)
				found = true
				break
			}
		}

		if !found {
			status.Status = "DeploymentNotFound"
			statuses = append(statuses, status)
			met = false
		}
	}

	for _, r := range requiredNativeAPIs {
		name := fmt.Sprintf("%s.%s", r.Version, r.Group)
		status := v1alpha1.RequirementStatus{
			Group:   r.Group,
			Version: r.Version,
			Kind:    r.Kind,
			Name:    name,
		}

		if err := a.isGVKRegistered(r.Group, r.Version, r.Kind); err != nil {
			status.Status = v1alpha1.RequirementStatusReasonNotPresent
			met = false
			statuses = append(statuses, status)
			continue
		} else {
			status.Status = v1alpha1.RequirementStatusReasonPresent
			statuses = append(statuses, status)
			continue
		}
	}

	return
}

// permissionStatus checks whether the given CSV's RBAC requirements are met in its namespace
func (a *Operator) permissionStatus(strategyDetailsDeployment *install.StrategyDetailsDeployment, ruleChecker install.RuleChecker, csvNamespace string) (bool, []v1alpha1.RequirementStatus, error) {
	statusesSet := map[string]v1alpha1.RequirementStatus{}
	met := true

	checkPermissions := func(permissions []install.StrategyDeploymentPermissions, namespace string) error {
		for _, perm := range permissions {
			saName := perm.ServiceAccountName
			a.Log.Debugf("perm.ServiceAccountName: %s", saName)

			var status v1alpha1.RequirementStatus
			if stored, ok := statusesSet[saName]; !ok {
				status = v1alpha1.RequirementStatus{
					Group:      "",
					Version:    "v1",
					Kind:       "ServiceAccount",
					Name:       saName,
					Status:     v1alpha1.RequirementStatusReasonPresent,
					Dependents: []v1alpha1.DependentStatus{},
				}
			} else {
				status = stored
			}

			// Ensure the ServiceAccount exists
			sa, err := a.OpClient.GetServiceAccount(csvNamespace, perm.ServiceAccountName)
			if err != nil {
				met = false
				status.Status = v1alpha1.RequirementStatusReasonNotPresent
				statusesSet[saName] = status
				continue
			}

			// Check if PolicyRules are satisfied
			for _, rule := range perm.Rules {
				dependent := v1alpha1.DependentStatus{
					Group:   "rbac.authorization.k8s.io",
					Kind:    "PolicyRule",
					Version: "v1beta1",
				}

				marshalled, err := json.Marshal(rule)
				if err != nil {
					dependent.Status = v1alpha1.DependentStatusReasonNotSatisfied
					dependent.Message = "rule unmarshallable"
					status.Dependents = append(status.Dependents, dependent)
					continue
				}

				var scope string
				if namespace == metav1.NamespaceAll {
					scope = "cluster"
				} else {
					scope = "namespaced"
				}
				dependent.Message = fmt.Sprintf("%s rule:%s", scope, marshalled)

				satisfied, err := ruleChecker.RuleSatisfied(sa, namespace, rule)
				if err != nil {
					return err
				} else if !satisfied {
					met = false
					dependent.Status = v1alpha1.DependentStatusReasonNotSatisfied
					status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
				} else {
					dependent.Status = v1alpha1.DependentStatusReasonSatisfied
				}

				status.Dependents = append(status.Dependents, dependent)
			}

			statusesSet[saName] = status
		}

		return nil
	}

	err := checkPermissions(strategyDetailsDeployment.Permissions, csvNamespace)
	if err != nil {
		return false, nil, err
	}
	err = checkPermissions(strategyDetailsDeployment.ClusterPermissions, metav1.NamespaceAll)
	if err != nil {
		return false, nil, err
	}

	statuses := []v1alpha1.RequirementStatus{}
	for key, status := range statusesSet {
		a.Log.Debugf("appending permission status: %s", key)
		statuses = append(statuses, status)
	}

	return met, statuses, nil
}

// requirementAndPermissionStatus returns the aggregate requirement and permissions statuses for the given CSV
func (a *Operator) requirementAndPermissionStatus(csv *v1alpha1.ClusterServiceVersion) (bool, []v1alpha1.RequirementStatus, error) {
	// Use a StrategyResolver to unmarshal
	strategyResolver := install.StrategyResolver{}
	strategy, err := strategyResolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		return false, nil, err
	}

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*install.StrategyDetailsDeployment)
	if !ok {
		return false, nil, fmt.Errorf("could not cast install strategy as type %T", strategyDetailsDeployment)
	}

	reqMet, reqStatuses := a.requirementStatus(strategyDetailsDeployment, csv.GetAllCRDDescriptions(), csv.GetOwnedAPIServiceDescriptions(), csv.GetRequiredAPIServiceDescriptions(), csv.Spec.NativeAPIs)

	rbacLister := a.lister.RbacV1()
	roleLister := rbacLister.RoleLister()
	roleBindingLister := rbacLister.RoleBindingLister()
	clusterRoleLister := rbacLister.ClusterRoleLister()
	clusterRoleBindingLister := rbacLister.ClusterRoleBindingLister()

	ruleChecker := install.NewCSVRuleChecker(roleLister, roleBindingLister, clusterRoleLister, clusterRoleBindingLister, csv)
	permMet, permStatuses, err := a.permissionStatus(strategyDetailsDeployment, ruleChecker, csv.GetNamespace())
	if err != nil {
		return false, nil, err
	}

	// Aggregate requirement and permissions statuses
	statuses := append(reqStatuses, permStatuses...)
	met := reqMet && permMet
	if !met {
		a.Log.Debugf("reqMet=%#v permMet=%v\n", reqMet, permMet)
	}

	return met, statuses, nil
}

func (a *Operator) isGVKRegistered(group, version, kind string) error {
	logger := a.Log.WithFields(logrus.Fields{
		"group":   group,
		"version": version,
		"kind":    kind,
	})

	gv := metav1.GroupVersion{Group: group, Version: version}
	resources, err := a.OpClient.KubernetesInterface().Discovery().ServerResourcesForGroupVersion(gv.String())
	if err != nil {
		logger.WithField("err", err).Info("could not query for GVK in api discovery")
		return err
	}

	for _, r := range resources.APIResources {
		if r.Kind == kind {
			return nil
		}
	}

	logger.Info("couldn't find GVK in api discovery")
	return olmErrors.GroupVersionKindNotFoundError{group, version, kind}
}
