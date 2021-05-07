package internal

import (
	"encoding/json"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"

	policyv1beta1 "k8s.io/api/policy/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var ObjectValidator interfaces.Validator = interfaces.ValidatorFunc(validateObjects)

const (
	PodDisruptionBudgetKind     = "PodDisruptionBudget"
	PriorityClassKind           = "PriorityClass"
	RoleKind                    = "Role"
	ClusterRoleKind             = "ClusterRole"
	PodDisruptionBudgetAPIGroup = "policy"
	SCCAPIGroup                 = "security.openshift.io"
)

// defaultSCCs is a map of the default Security Context Constraints present as of OpenShift 4.5.
// See https://docs.openshift.com/container-platform/4.5/authentication/managing-security-context-constraints.html#security-context-constraints-about_configuring-internal-oauth
var defaultSCCs = map[string]struct{}{
	"privileged":       {},
	"restricted":       {},
	"anyuid":           {},
	"hostaccess":       {},
	"hostmount-anyuid": {},
	"hostnetwork":      {},
	"node-exporter":    {},
	"nonroot":          {},
}

func validateObjects(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch u := obj.(type) {
		case *unstructured.Unstructured:
			switch u.GroupVersionKind().Kind {
			case PodDisruptionBudgetKind:
				results = append(results, validatePDB(u))
			case PriorityClassKind:
				results = append(results, validatePriorityClass(u))
			case RoleKind:
				results = append(results, validateRBAC(u))
			case ClusterRoleKind:
				results = append(results, validateRBAC(u))
			}
		}
	}
	return results
}

// validatePDB checks the PDB to ensure the minimum and maximum budgets are set to reasonable levels.
// See https://github.com/operator-framework/operator-lifecycle-manager/blob/master/doc/design/adding-pod-disruption-budgets.md#limitations-on-pod-disruption-budgets
func validatePDB(u *unstructured.Unstructured) (result errors.ManifestResult) {
	pdb := policyv1beta1.PodDisruptionBudget{}

	b, err := u.MarshalJSON()
	if err != nil {
		result.Add(errors.ErrInvalidParse("error converting unstructured", err))
		return
	}

	err = json.Unmarshal(b, &pdb)
	if err != nil {
		result.Add(errors.ErrInvalidParse("error unmarshaling poddisruptionbudget", err))
		return
	}

	/*
	   maxUnavailable field cannot be set to 0 or 0%.
	   minAvailable field cannot be set to 100%.
	*/

	maxUnavailable := pdb.Spec.MaxUnavailable
	if maxUnavailable != nil && (maxUnavailable.IntVal == 0 || maxUnavailable.StrVal == "0%") {
		result.Add(errors.ErrInvalidObject(pdb, "maxUnavailable field cannot be set to 0 or 0%"))
	}

	minAvailable := pdb.Spec.MinAvailable
	if minAvailable != nil && minAvailable.StrVal == "100%" {
		result.Add(errors.ErrInvalidObject(pdb, "minAvailable field cannot be set to 100%"))
	}

	return
}

// validatePriorityClass checks the PriorityClass object to ensure globalDefault is set to false.
// See https://github.com/operator-framework/operator-lifecycle-manager/blob/master/doc/design/adding-priority-classes.md
func validatePriorityClass(u *unstructured.Unstructured) (result errors.ManifestResult) {
	pc := schedulingv1.PriorityClass{}

	b, err := u.MarshalJSON()
	if err != nil {
		result.Add(errors.ErrInvalidParse("error converting unstructured", err))
		return
	}

	err = json.Unmarshal(b, &pc)
	if err != nil {
		result.Add(errors.ErrInvalidParse("error unmarshaling priorityclass", err))
		return
	}

	if pc.GlobalDefault {
		result.Add(errors.ErrInvalidObject(pc, "globalDefault field cannot be set to true"))
	}

	return
}

func validateRBAC(u *unstructured.Unstructured) (result errors.ManifestResult) {
	var policyRules []rbacv1.PolicyRule

	b, err := u.MarshalJSON()
	if err != nil {
		result.Add(errors.ErrInvalidParse("error converting unstructured", err))
		return
	}

	switch u.GroupVersionKind().Kind {
	case RoleKind:
		role := rbacv1.Role{}
		err = json.Unmarshal(b, &role)
		if err != nil {
			result.Add(errors.ErrInvalidParse("error unmarshaling role", err))
			return
		}
		policyRules = role.Rules
	case ClusterRoleKind:
		clusterrole := rbacv1.ClusterRole{}
		err = json.Unmarshal(b, &clusterrole)
		if err != nil {
			result.Add(errors.ErrInvalidParse("error unmarshaling clusterrole", err))
			return
		}
		policyRules = clusterrole.Rules
	}

	return audit(policyRules)
}

// audit checks the provided rbac policies against prescribed limitations.
// If permission is granted to create/modify a PDB, a warning is returned.
// If permission is granted to modify default SCCs in OpenShift, an error is returned.
func audit(policies []rbacv1.PolicyRule) (result errors.ManifestResult) {
	// check for permission to modify/create PDBs
	for _, rule := range policies {
		if contains(rule.APIGroups, PodDisruptionBudgetAPIGroup) &&
			contains(rule.Resources, "poddisruptionbudgets") &&
			contains(rule.Verbs, rbacv1.VerbAll, "create", "update", "patch") {
			result.Add(errors.WarnInvalidObject("RBAC includes permission to create/update poddisruptionbudgets, which could impact cluster stability", rule))
		}
	}

	// check sccs for modifying default known SCCs
	for _, rule := range policies {
		if contains(rule.APIGroups, SCCAPIGroup) &&
			contains(rule.Resources, "securitycontextconstraints") &&
			contains(rule.Verbs, rbacv1.VerbAll, "delete", "update", "patch") &&
			containsDefaults(rule.ResourceNames, defaultSCCs) {
			result.Add(errors.ErrInvalidObject(rule, "RBAC includes permission to modify default securitycontextconstraints, which could impact cluster stability"))
		}
	}

	return
}

// contains returns true if at least one item is present in the array
func contains(slice []string, items ...string) bool {
	set := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		set[s] = struct{}{}
	}

	for _, item := range items {
		if _, ok := set[item]; ok {
			return true
		}
	}

	return false
}

// containsDefaults returns true if at least one item is present as a key in the map
func containsDefaults(slice []string, defaults map[string]struct{}) bool {
	for _, s := range slice {
		if _, ok := defaults[s]; ok {
			return true
		}
	}
	return false
}
