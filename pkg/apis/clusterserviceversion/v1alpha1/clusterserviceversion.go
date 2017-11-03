package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
