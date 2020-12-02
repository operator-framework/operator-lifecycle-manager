package olm

import (
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func (a *Operator) isOperatorUpgradeable(csv *v1alpha1.ClusterServiceVersion) (bool, error) {
	if csv == nil {
		return false, fmt.Errorf("CSV is invalid")
	}

	cond, err := a.lister.OperatorsV1().OperatorConditionLister().OperatorConditions(csv.GetNamespace()).Get(csv.GetName())
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}

	// Check condition overrides
	for _, override := range cond.Spec.Overrides {
		if override.Type == operatorsv1.OperatorUpgradeable {
			if override.Status == metav1.ConditionTrue {
				return true, nil
			}
			return false, fmt.Errorf("The operator is not upgradeable: %s", override.Message)
		}
	}

	// Check for OperatorUpgradeable condition status
	if c := meta.FindStatusCondition(cond.Status.Conditions, operatorsv1.OperatorUpgradeable); c != nil {
		if c.Status == metav1.ConditionFalse {
			return false, fmt.Errorf("The operator is not upgradeable: %s", c.Message)
		}
	}

	return true, nil
}
