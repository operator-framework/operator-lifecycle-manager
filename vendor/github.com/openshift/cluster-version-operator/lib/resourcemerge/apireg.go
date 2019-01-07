package resourcemerge

import (
	"k8s.io/apimachinery/pkg/api/equality"
	apiregv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

// EnsureAPIService ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureAPIService(modified *bool, existing *apiregv1.APIService, required apiregv1.APIService) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	// we stomp everything
	if !equality.Semantic.DeepEqual(existing.Spec, required.Spec) {
		*modified = true
		existing.Spec = required.Spec
	}
}
