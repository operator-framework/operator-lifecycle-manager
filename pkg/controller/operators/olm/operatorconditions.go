package olm

import (
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv2 "github.com/operator-framework/api/pkg/operators/v2"
	"github.com/sirupsen/logrus"
)

func (a *Operator) isOperatorUpgradeable(csv *v1alpha1.ClusterServiceVersion) (bool, error) {
	if csv == nil {
		return false, fmt.Errorf("CSV is invalid")
	}

	cond, err := a.lister.OperatorsV2().OperatorConditionLister().OperatorConditions(csv.GetNamespace()).Get(csv.GetName())
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
	if o := meta.FindStatusCondition(cond.Spec.Overrides, operatorsv2.Upgradeable); o != nil {
		if o.Status == metav1.ConditionTrue {
			logger.Infof("Upgradeable condition is overridden to true: %s", o.Message)
			return true, nil
		}
		logger.Infof("Upgradeable condition is overridden to false: %s", o.Message)
		return false, fmt.Errorf("The operator is not upgradeable: %s", o.Message)
	}

	// Check for OperatorUpgradeable condition status
	if c := meta.FindStatusCondition(cond.Status.Conditions, operatorsv2.Upgradeable); c != nil {
		if c.ObservedGeneration != cond.ObjectMeta.Generation {
			logger.Debugf("Upgradeable condition's generation doesn't match: %d/%d", c.ObservedGeneration, cond.ObjectMeta.Generation)
			return false, fmt.Errorf("The operatorcondition status %q=%q is outdated", c.Type, c.Status)
		}
		if c.Status == metav1.ConditionFalse {
			return false, fmt.Errorf("The operator is not upgradeable: %s", c.Message)
		}
	}

	return true, nil
}
