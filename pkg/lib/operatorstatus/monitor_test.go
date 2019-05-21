package operatorstatus

import (
	"fmt"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
)

func TestMonitorWaiting(t *testing.T) {
	fakeClock := clock.NewFakeClock(time.Now())
	name := "foo"

	statusWant := &configv1.ClusterOperatorStatus{
		Conditions: []configv1.ClusterOperatorStatusCondition{
			configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorDegraded,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: metav1.NewTime(fakeClock.Now()),
			},
			configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorAvailable,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: metav1.NewTime(fakeClock.Now()),
			},
			configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				Message:            fmt.Sprintf("waiting for events - source=%s", name),
				LastTransitionTime: metav1.NewTime(fakeClock.Now()),
			},
		},
		Versions:       []configv1.OperandVersion{},
		RelatedObjects: []configv1.ObjectReference{},
	}

	statusGot := Waiting(fakeClock, name)

	assert.Equal(t, statusWant, statusGot)
}
