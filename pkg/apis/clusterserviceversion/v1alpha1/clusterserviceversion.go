package v1alpha1

import (
	"fmt"

	"github.com/coreos-inc/alm/pkg/install"
	rbac "k8s.io/api/rbac/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/apis/apps"
	"k8s.io/kubernetes/pkg/apis/extensions"
)

var baseRules []rbac.PolicyRule

func init() {
	// need to write deployments
	baseRules = append(baseRules, rbac.NewRule(rbac.VerbAll).Groups(extensions.GroupName, apps.GroupName).Resources("deployments").RuleOrDie())

	// neeed to write roles, rolebindings, serviceaccounts, secrets
	baseRules = append(baseRules, rbac.NewRule(rbac.VerbAll).Groups(rbac.APIGroupAll).Resources("roles", "rolebindings", "serviceaccounts", "secrets").RuleOrDie())
}

func (c *ClusterServiceVersion) SetPhase(phase ClusterServiceVersionPhase, reason ConditionReason, message string) {
	c.Status.LastUpdateTime = metav1.Now()
	if c.Status.Phase != phase {
		c.Status.Phase = phase
		c.Status.LastTransitionTime = metav1.Now()
	}
	c.Status.Message = message
	c.Status.Reason = reason
	if len(c.Status.Conditions) == 0 {
		c.Status.Conditions = append(c.Status.Conditions, ClusterServiceVersionCondition{
			Phase:              c.Status.Phase,
			LastTransitionTime: c.Status.LastTransitionTime,
			LastUpdateTime:     c.Status.LastUpdateTime,
			Message:            message,
			Reason:             reason,
		})
	}
	previousCondition := c.Status.Conditions[len(c.Status.Conditions)-1]
	if previousCondition.Phase != c.Status.Phase || previousCondition.Reason != c.Status.Reason {
		c.Status.Conditions = append(c.Status.Conditions, ClusterServiceVersionCondition{
			Phase:              c.Status.Phase,
			LastTransitionTime: c.Status.LastTransitionTime,
			LastUpdateTime:     c.Status.LastUpdateTime,
			Message:            message,
			Reason:             reason,
		})
	}
}

func (c *ClusterServiceVersion) SetRequirementStatus(statuses []RequirementStatus) {
	c.Status.RequirementStatus = statuses
}

func (c *ClusterServiceVersion) GetRoleRules() ([]rbac.PolicyRule, error) {
	// need base rules to perform base operator actions
	rules := append([]rbac.PolicyRule{}, baseRules...)

	resolver := install.StrategyResolver{}
	strategy, err := resolver.UnmarshalStrategy(c.Spec.InstallStrategy)
	if err != nil {
		return nil, err
	}

	switch strategy.GetStrategyName() {
	case install.InstallStrategyNameDeployment:
		depStrategy, ok := strategy.(*install.StrategyDetailsDeployment)
		if !ok {
			return nil, fmt.Errorf("couldn't deserializae strategy details as deployment strategy: %v", depStrategy)
		}

		// need all rules from all serviceaccounts, so that the operator can create the serviceaccount
		for _, perm := range depStrategy.Permissions {
			rules = append(rules, perm.Rules...)
		}
	}

	return rules, nil
}
