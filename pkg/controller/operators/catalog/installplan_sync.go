package catalog

import (
	"context"
	"errors"


	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/scoped"
)

// When a user adds permission to a ServiceAccount by creating or updating
// Role/RoleBinding then we expect the InstallPlan that refers to the
// ServiceAccount to be retried if it has failed to install before due to
// permission issue(s).
func (o *Operator) triggerInstallPlanRetry(obj interface{}) (syncError error) {
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		syncError = errors.New("casting to metav1 object failed")
		o.logger.Warn(syncError.Error())
		return
	}

	related, _ := scoped.IsObjectRBACRelated(obj)
	if !related {
		return
	}

	ips, err := o.lister.OperatorsV1alpha1().InstallPlanLister().InstallPlans(metaObj.GetNamespace()).List(labels.Everything())
	if err != nil {
		syncError = err
		return
	}

	isTarget := func(ip *v1alpha1.InstallPlan) bool {
		// Only an InstallPlan that has failed to install before and only if it
		// has a reference to a ServiceAccount then
		return ip.Status.Phase == v1alpha1.InstallPlanPhaseFailed && ip.Status.AttenuatedServiceAccountRef != nil
	}

	update := func(ip *v1alpha1.InstallPlan) error {
		out := ip.DeepCopy()
		out.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
		_, err := o.client.OperatorsV1alpha1().InstallPlans(ip.GetNamespace()).UpdateStatus(context.TODO(), out, metav1.UpdateOptions{})

		return err
	}

	var errs []error
	for _, ip := range ips {
		if !isTarget(ip) {
			continue
		}

		logger := o.logger.WithFields(logrus.Fields{
			"ip":        ip.GetName(),
			"namespace": ip.GetNamespace(),
			"phase":     ip.Status.Phase,
		})

		if updateErr := update(ip); updateErr != nil {
			errs = append(errs, updateErr)
			logger.WithError(updateErr).Warn("failed to kick off InstallPlan retry")
			continue
		}

		logger.Info("InstallPlan status set to 'Installing' for retry")
	}

	syncError = utilerrors.NewAggregate(errs)
	return
}
