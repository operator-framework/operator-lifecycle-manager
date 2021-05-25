package olm

import (
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/sirupsen/logrus"
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

	logger := a.logger.WithFields(logrus.Fields{
		"name":      csv.GetName(),
		"namespace": csv.GetNamespace(),
	})

	// Check condition overrides
	for _, override := range cond.Spec.Overrides {
		if override.Type == operatorsv1.Upgradeable {
			if override.Status == metav1.ConditionTrue {
				logger.Infof("Upgradeable condition is overridden to true: %s", override.Message)
				return true, nil
			}
			logger.Infof("Upgradeable condition is overridden to false: %s", override.Message)
			return false, fmt.Errorf("The operator is not upgradeable: %s", override.Message)
		}
	}

	// Check for OperatorUpgradeable condition status
	if c := meta.FindStatusCondition(cond.Status.Conditions, operatorsv1.Upgradeable); c != nil {
		if c.ObservedGeneration < cond.ObjectMeta.Generation {
			logger.Debugf("Upgradeable condition's generation doesn't match: %d/%d", c.ObservedGeneration, cond.ObjectMeta.Generation)
			return false, fmt.Errorf("The operatorcondition status %q=%q is outdated", c.Type, c.Status)
		}
		if c.Status == metav1.ConditionFalse {
			return false, fmt.Errorf("The operator is not upgradeable: %s", c.Message)
		}
	}

	return true, nil
}
