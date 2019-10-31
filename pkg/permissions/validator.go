package permissions

import (
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

type NamespaceRule struct {
	Namespace  string
	PolicyRule rbacv1.PolicyRule
}

type Validator interface {
	UserCanCreateV1Alpha1CSV(username string, csv *v1alpha1.ClusterServiceVersion) error
}

var _ Validator = PermissionValidator{}

type PermissionValidator struct {
	lister operatorlister.OperatorLister
}

func NewPermissionValidator(lister operatorlister.OperatorLister) *PermissionValidator {
	return &PermissionValidator{lister: lister}
}

// TODO: use impersonation + dry-run?
func (p PermissionValidator) UserCanCreateV1Alpha1CSV(username string, csv *v1alpha1.ClusterServiceVersion) error {
	ruleChecker := NewCSVRuleChecker(
		p.lister.RbacV1().RoleLister(),
		p.lister.RbacV1().RoleBindingLister(),
		p.lister.RbacV1().ClusterRoleLister(),
		p.lister.RbacV1().ClusterRoleBindingLister(),
		csv)

	operatorPermissions, err := resolver.RBACForClusterServiceVersion(csv)
	if err != nil {
		return fmt.Errorf("failed to get rbac from csv for generation")
	}

	rules := ToNamespaceRules(csv.GetNamespace(), operatorPermissions)
	rules = WithoutOwnedAndRequired(csv, rules)

	errs := []error{}
	for _, rule := range rules {
		if err := ruleChecker.RuleSatisfiedUser(username, rule.Namespace, rule.PolicyRule); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.NewAggregate(errs)
}

func ToNamespaceRules(namespace string, perms map[string]*resolver.OperatorPermissions) (rules []NamespaceRule) {
	for _, p := range perms {
		for _, role := range p.Roles {
			for _, rule := range role.Rules {
				rules = append(rules, NamespaceRule{
					Namespace:  namespace,
					PolicyRule: rule,
				})
			}
		}
		for _, role := range p.ClusterRoles {
			for _, rule := range role.Rules {
				rules = append(rules, NamespaceRule{
					Namespace:  v1.NamespaceAll,
					PolicyRule: rule,
				})
			}
		}
	}
	return
}

// TODO: is group skipping sufficient? what if a rule lists multiple groups, only one of which matches?
func WithoutOwnedAndRequired(csv *v1alpha1.ClusterServiceVersion, rules []NamespaceRule) (filtered []NamespaceRule) {
	for _, nrule := range rules {
		// skip any rule that matches an owned or required CRD group
		for _, desc := range csv.GetAllCRDDescriptions() {
			for _, g := range nrule.PolicyRule.APIGroups {
				parts := strings.SplitN(desc.Name, ".", 2)
				if len(parts) > 2 || g == parts[1] {
					continue
				}
			}
		}

		// skip any rule that matches an owned or required api group
		for _, desc := range csv.GetAllAPIServiceDescriptions() {
			for _, g := range nrule.PolicyRule.APIGroups {
				if g == desc.Group {
					continue
				}
			}
		}
		filtered = append(filtered, nrule)
	}
	return
}
