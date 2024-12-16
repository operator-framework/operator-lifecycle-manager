package subscription

import (
	"bytes"
	"context"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// installPlanReconciler reconciles InstallPlan status for Subscriptions.
type installPlanReconciler struct {
	now               func() *metav1.Time
	installPlanLister listers.InstallPlanLister
}

func (i *installPlanReconciler) Reconcile(_ context.Context, sub *v1alpha1.Subscription) (*v1alpha1.Subscription, error) {
	out := sub.DeepCopy()

	// Check the stated InstallPlan - bail if not set
	if sub.Status.InstallPlanRef == nil {
		return out, nil
	}
	ref := sub.Status.InstallPlanRef // Should never be nil in this typestate
	plan, err := i.installPlanLister.InstallPlans(ref.Namespace).Get(ref.Name)
	now := i.now()
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Remove pending and failed conditions
			out.Status.RemoveConditions(v1alpha1.SubscriptionInstallPlanPending, v1alpha1.SubscriptionInstallPlanFailed)

			// If the installplan is missing when subscription is in pending upgrade,
			// clear the installplan ref so the resolution can happen again
			if sub.Status.State == v1alpha1.SubscriptionStateUpgradePending {
				out.Status.InstallPlanRef = nil
				out.Status.Install = nil
				out.Status.CurrentCSV = ""
				out.Status.State = v1alpha1.SubscriptionStateNone
				out.Status.LastUpdated = *now
			} else {
				// Set missing condition to true
				origCond := sub.Status.GetCondition(v1alpha1.SubscriptionInstallPlanMissing)
				cond := out.Status.GetCondition(v1alpha1.SubscriptionInstallPlanMissing)
				cond.Status = corev1.ConditionTrue
				cond.Reason = v1alpha1.ReferencedInstallPlanNotFound
				cond.LastTransitionTime = i.now()
				if cond.Reason != origCond.Reason || cond.Message != origCond.Message || cond.Status != origCond.Status {
					out.Status.SetCondition(cond)
					out.Status.LastUpdated = *now
				}
			}
			return out, nil
		}
		return nil, err
	}

	// Remove missing, pending, and failed conditions
	out.Status.RemoveConditions(v1alpha1.SubscriptionInstallPlanMissing, v1alpha1.SubscriptionInstallPlanPending, v1alpha1.SubscriptionInstallPlanFailed)

	// Build and set the InstallPlan condition, if any
	cond := v1alpha1.SubscriptionCondition{
		Status:             corev1.ConditionUnknown,
		LastTransitionTime: i.now(),
	}

	// TODO: Use InstallPlan conditions instead of phases
	// Get the status of the InstallPlan and create the appropriate condition and state
	switch phase := plan.Status.Phase; phase {
	case v1alpha1.InstallPlanPhaseNone:
		// Set reason and let the following case fill out the pending condition
		cond.Reason = v1alpha1.InstallPlanNotYetReconciled
		fallthrough
	case v1alpha1.InstallPlanPhasePlanning, v1alpha1.InstallPlanPhaseInstalling, v1alpha1.InstallPlanPhaseRequiresApproval:
		if cond.Reason == "" {
			cond.Reason = string(phase)
		}
		cond.Message = extractMessage(&plan.Status)
		cond.Type = v1alpha1.SubscriptionInstallPlanPending
		cond.Status = corev1.ConditionTrue
		oldCond := sub.Status.GetCondition(v1alpha1.SubscriptionInstallPlanPending)
		if !cond.Equals(oldCond) {
			out.Status.SetCondition(cond)
			out.Status.LastUpdated = *now
		} else {
			out.Status.SetCondition(oldCond)
		}
	case v1alpha1.InstallPlanPhaseFailed:
		// Attempt to project reason from failed InstallPlan condition
		if installedCond := plan.Status.GetCondition(v1alpha1.InstallPlanInstalled); installedCond.Status == corev1.ConditionFalse {
			cond.Reason = string(installedCond.Reason)
		} else {
			cond.Reason = v1alpha1.InstallPlanFailed
		}

		cond.Type = v1alpha1.SubscriptionInstallPlanFailed
		cond.Message = extractMessage(&plan.Status)
		cond.Status = corev1.ConditionTrue
		oldCond := sub.Status.GetCondition(v1alpha1.SubscriptionInstallPlanFailed)
		if !cond.Equals(oldCond) {
			out.Status.SetCondition(cond)
			out.Status.LastUpdated = *now
		} else {
			out.Status.SetCondition(oldCond)
		}
	}
	return out, nil
}

func extractMessage(status *v1alpha1.InstallPlanStatus) string {
	if cond := status.GetCondition(v1alpha1.InstallPlanInstalled); cond.Status != corev1.ConditionUnknown && cond.Message != "" {
		return cond.Message
	}

	var b bytes.Buffer
	for _, lookup := range status.BundleLookups {
		if cond := lookup.GetCondition(v1alpha1.BundleLookupPending); cond.Status != corev1.ConditionUnknown && cond.Message != "" {
			if b.Len() != 0 {
				b.WriteString(" ")
			}
			b.WriteString(cond.Message)
			b.WriteString(".")
		}
	}

	return b.String()
}
