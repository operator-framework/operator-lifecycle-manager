package install

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	crbacv1 "k8s.io/client-go/listers/rbac/v1"
	rbacauthorizer "k8s.io/kubernetes/plugin/pkg/auth/authorizer/rbac"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

// RuleChecker is used to verify whether PolicyRules are satisfied by existing Roles or ClusterRoles
type RuleChecker interface {
	// RuleSatisfied determines whether a PolicyRule is satisfied for a ServiceAccount
	// by existing Roles and ClusterRoles
	RuleSatisfied(sa *corev1.ServiceAccount, namespace string, rule rbacv1.PolicyRule) (bool, error)
}

// CSVRuleChecker determines whether a PolicyRule is satisfied for a ServiceAccount
// by existing Roles and ClusterRoles
type CSVRuleChecker struct {
	roleLister               crbacv1.RoleLister
	roleBindingLister        crbacv1.RoleBindingLister
	clusterRoleLister        crbacv1.ClusterRoleLister
	clusterRoleBindingLister crbacv1.ClusterRoleBindingLister
	csv                      *v1alpha1.ClusterServiceVersion
}

// NewCSVRuleChecker returns a pointer to a new CSVRuleChecker
func NewCSVRuleChecker(roleLister crbacv1.RoleLister, roleBindingLister crbacv1.RoleBindingLister, clusterRoleLister crbacv1.ClusterRoleLister, clusterRoleBindingLister crbacv1.ClusterRoleBindingLister, csv *v1alpha1.ClusterServiceVersion) *CSVRuleChecker {
	return &CSVRuleChecker{
		roleLister:               roleLister,
		roleBindingLister:        roleBindingLister,
		clusterRoleLister:        clusterRoleLister,
		clusterRoleBindingLister: clusterRoleBindingLister,
		csv:                      csv.DeepCopy(),
	}
}

// RuleSatisfied returns true if a ServiceAccount is authorized to perform all actions described by a PolicyRule in a namespace
func (c *CSVRuleChecker) RuleSatisfied(sa *corev1.ServiceAccount, namespace string, rule rbacv1.PolicyRule) (bool, error) {
	// get attributes set for the given Role and ServiceAccount
	user := toDefaultInfo(sa)
	attributesSet := toAttributesSet(user, namespace, rule)

	// create a new RBACAuthorizer
	rbacAuthorizer := rbacauthorizer.New(c, c, c, c)

	// ensure all attributes are authorized
	for _, attributes := range attributesSet {
		decision, _, err := rbacAuthorizer.Authorize(attributes)
		if err != nil {
			return false, err
		}

		if decision == authorizer.DecisionDeny || decision == authorizer.DecisionNoOpinion {
			return false, nil
		}

	}

	return true, nil
}

func (c *CSVRuleChecker) GetRole(namespace, name string) (*rbacv1.Role, error) {
	// get the Role
	role, err := c.roleLister.Roles(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	// check if the Role has an OwnerConflict with the client's CSV
	if role != nil && c.hasOwnerConflicts(role.GetOwnerReferences()) {
		return &rbacv1.Role{}, nil
	}

	return role, nil
}

func (c *CSVRuleChecker) ListRoleBindings(namespace string) ([]*rbacv1.RoleBinding, error) {
	// get all RoleBindings
	rbList, err := c.roleBindingLister.RoleBindings(namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	// filter based on OwnerReferences
	var filtered []*rbacv1.RoleBinding
	for _, rb := range rbList {
		if !c.hasOwnerConflicts(rb.GetOwnerReferences()) {
			filtered = append(filtered, rb)
		}
	}

	return filtered, nil
}

func (c *CSVRuleChecker) GetClusterRole(name string) (*rbacv1.ClusterRole, error) {
	// get the ClusterRole
	clusterRole, err := c.clusterRoleLister.Get(name)
	if err != nil {
		return nil, err
	}

	// check if the ClusterRole has an OwnerConflict with the client's CSV
	if clusterRole != nil && c.hasOwnerConflicts(clusterRole.GetOwnerReferences()) {
		return &rbacv1.ClusterRole{}, nil
	}

	return clusterRole, nil
}

func (c *CSVRuleChecker) ListClusterRoleBindings() ([]*rbacv1.ClusterRoleBinding, error) {
	// get all RoleBindings
	crbList, err := c.clusterRoleBindingLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	// filter based on OwnerReferences
	var filtered []*rbacv1.ClusterRoleBinding
	for _, crb := range crbList {
		if !c.hasOwnerConflicts(crb.GetOwnerReferences()) {
			filtered = append(filtered, crb)
		}
	}

	return filtered, nil
}

// hasOwnerConflicts checks if the given list of OwnerReferences points to CSVs other than the
// CSVRuleChecker's. The method returns true if the list of OwnerReferences contains elements of Kind
// ClusterServiceVersion but does not include an OwnerReference to the CSVRuleChecker's CSV. If there
// are no OwnerReferences of Kind ClusterServiceVersion, or there is but one element is an OwnerReference
// to the CSVRuleChecker's CSV, then the method returns false.
//
// Note: This is imporant when determining if a Role, RoleBinding, ClusterRole, or ClusterRoleBinding
// can be used to satisfy permissions of a CSV. If the CSVRuleChecker's CSV is not a member of the RBAC resource's
// OwnerReferences, then we know the resource can be garbage collected by OLM independently of the CSVRuleChecker's
// CSV
func (c *CSVRuleChecker) hasOwnerConflicts(ownerRefs []metav1.OwnerReference) bool {
	conflicts := false
	for _, ownerRef := range ownerRefs {
		if ownerRef.Kind == v1alpha1.ClusterServiceVersionKind {
			if ownerRef.Name == c.csv.GetName() && ownerRef.UID == c.csv.GetUID() {
				return false
			}

			conflicts = true
		}
	}

	return conflicts
}
