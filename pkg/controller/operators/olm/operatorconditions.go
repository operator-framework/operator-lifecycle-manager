package olm

import (
	"context"
	"fmt"

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

	if len(cond.Status.Conditions) < 1 {
		return false, fmt.Errorf("No operator conditions are available")
	}
  if upgradeCond := cond.GetCondition(operatorv1.OperatorUpgradeable); upgradeCond.Status == corev1.ConditionFalse {
    return false, fmt.Errorf("The operator is not upgradeable: %s", upgradeCond.Message)
  }

  return true, nil
}
