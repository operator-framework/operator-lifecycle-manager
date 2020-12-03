package olm

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func (a *Operator) isOperatorUpgradeable(csv *v1alpha1.ClusterServiceVersion) (bool, error) {
	if csv == nil {
		return false, fmt.Errorf("CSV is invalid")
	}

	cond, err := a.lister.OperatorsV1().OperatorConditions(csv.GetNamespace()).Get(context.TODO(), csv.GetName(), metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}

	// Check condition overrides
	for _, override := range cond.Spec.Overrides {
		if override.Type == operatorv1.OperatorUpgradeable {
			if override.Status == corev1.ConditionTrue {
				return true, nil
			}
			return false, fmt.Errorf("The operator is not upgradeable: %s", override.Message)
		}
	}

	// If no condition, proceed with normal flow
	if len(cond.Status.Conditions) < 1 {
		return true, nil
	}
	if v1.IsStatusConditionFalse(cond.Status.Conditions, operatorv1.OperatorUpgradeable) {
		return false, fmt.Errorf("The operator is not upgradeable")
	}

	return true, nil
}
