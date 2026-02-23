package openshift

import (
	"context"

	configv1 "github.com/openshift/api/config/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func NewClusterOperator(name string) *ClusterOperator {
	co := &ClusterOperator{ClusterOperator: &configv1.ClusterOperator{}}
	co.SetName(name)
	return co
}

type ClusterOperator struct {
	*configv1.ClusterOperator
}

func (c *ClusterOperator) GetOperatorVersion() string {
	for _, v := range c.Status.Versions {
		if v.Name == "operator" {
			return v.Version
		}
	}

	return ""
}

func (c *ClusterOperator) GetCondition(conditionType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for _, cond := range c.Status.Conditions {
		if cond.Type == conditionType {
			return &cond
		}
	}

	return nil
}

func (c *ClusterOperator) SetCondition(condition *configv1.ClusterOperatorStatusCondition) {
	// Filter dups
	conditions := []configv1.ClusterOperatorStatusCondition{}
	for _, c := range c.Status.Conditions {
		if c.Type != condition.Type {
			conditions = append(conditions, c)
		}
	}

	c.Status.Conditions = append(conditions, *condition)
}

type Mutator interface {
	Mutate(context.Context, *ClusterOperator) error
}

type MutateFunc func(context.Context, *ClusterOperator) error

func (m MutateFunc) Mutate(ctx context.Context, co *ClusterOperator) error {
	return m(ctx, co)
}

type SerialMutations []Mutator

func (s SerialMutations) Mutate(ctx context.Context, co *ClusterOperator) error {
	var errs []error
	for _, m := range s {
		if err := m.Mutate(ctx, co); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}
