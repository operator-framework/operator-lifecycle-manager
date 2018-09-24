package install

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	rbacauthorizer "k8s.io/kubernetes/plugin/pkg/auth/authorizer/rbac"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
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
	opClient operatorclient.ClientInterface
	csv      *v1alpha1.ClusterServiceVersion
}

func NewCSVRuleChecker(opClient operatorclient.ClientInterface, csv *v1alpha1.ClusterServiceVersion) *CSVRuleChecker {
	return &CSVRuleChecker{
		opClient: opClient,
		csv:      csv.DeepCopy(),
	}
}

// RuleSatisfied returns true if a ServiceAccount is authorized to perform all actions described by a PolicyRule in a namespace
func (c *CSVRuleChecker) RuleSatisfied(sa *corev1.ServiceAccount, namespace string, rule rbacv1.PolicyRule) (bool, error) {
	// get attributes set for the given Role and ServiceAccount
	user := toDefaultInfo(sa, namespace)
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
	role, err := c.opClient.KubernetesInterface().RbacV1().Roles(namespace).Get(name, metav1.GetOptions{})
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
	rbList, err := c.opClient.KubernetesInterface().RbacV1().RoleBindings(namespace).List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// filter based on OwnerReferences
	var filtered []*rbacv1.RoleBinding
	for i := 0; i < len(rbList.Items); i++ {
		if !c.hasOwnerConflicts(rbList.Items[i].GetOwnerReferences()) {
			filtered = append(filtered, &rbList.Items[i])
		}
	}

	return filtered, nil
}

func (c *CSVRuleChecker) GetClusterRole(name string) (*rbacv1.ClusterRole, error) {
	// get the ClusterRole
	clusterRole, err := c.opClient.KubernetesInterface().RbacV1().ClusterRoles().Get(name, metav1.GetOptions{})
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
	crbList, err := c.opClient.KubernetesInterface().RbacV1().ClusterRoleBindings().List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// filter based on OwnerReferences
	var filtered []*rbacv1.ClusterRoleBinding
	for i := 0; i < len(crbList.Items); i++ {
		item := crbList.Items[i]
		if !c.hasOwnerConflicts(item.GetOwnerReferences()) {
			filtered = append(filtered, &item)
		}
	}

	return filtered, nil
}

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
