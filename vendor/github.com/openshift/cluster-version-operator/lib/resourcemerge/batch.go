package resourcemerge

import (
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureJob ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureJob(modified *bool, existing *batchv1.Job, required batchv1.Job) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	if existing.Spec.Selector == nil {
		*modified = true
		existing.Spec.Selector = required.Spec.Selector
	}
	if !equality.Semantic.DeepEqual(existing.Spec.Selector, required.Spec.Selector) {
		*modified = true
		existing.Spec.Selector = required.Spec.Selector
	}
	setInt32Ptr(modified, &existing.Spec.Parallelism, required.Spec.Parallelism)
	setInt32Ptr(modified, &existing.Spec.Completions, required.Spec.Completions)
	setInt64Ptr(modified, &existing.Spec.ActiveDeadlineSeconds, required.Spec.ActiveDeadlineSeconds)
	setInt32Ptr(modified, &existing.Spec.BackoffLimit, required.Spec.BackoffLimit)
	setBoolPtr(modified, &existing.Spec.ManualSelector, required.Spec.ManualSelector)

	ensurePodTemplateSpec(modified, &existing.Spec.Template, required.Spec.Template)
}
