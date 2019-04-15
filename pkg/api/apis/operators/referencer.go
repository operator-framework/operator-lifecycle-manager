package operators

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/references"
)

var _ references.ObjectReferencerFunc = ObjectReferenceFor

// ObjectReferenceFor generates an ObjectReference for the given resource if it's provided by the operators.coreos.com API group.
func ObjectReferenceFor(obj interface{}) (*corev1.ObjectReference, error) {
	// Attempt to access ObjectMeta
	objMeta, err := meta.Accessor(obj)
	if err != nil {
		return nil, references.NewCannotReferenceError(obj, err.Error())
	}

	ref := &corev1.ObjectReference{
		Namespace: objMeta.GetNamespace(),
		Name:      objMeta.GetName(),
		UID:       objMeta.GetUID(),
	}
	switch objMeta.(type) {
	case *v1alpha1.ClusterServiceVersion:
		ref.Kind = v1alpha1.ClusterServiceVersionKind
		ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
	case *v1alpha1.InstallPlan:
		ref.Kind = v1alpha1.InstallPlanKind
		ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
	case *v1alpha1.Subscription:
		ref.Kind = v1alpha1.SubscriptionKind
		ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
	case *v1alpha1.CatalogSource:
		ref.Kind = v1alpha1.CatalogSourceKind
		ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
	case *v1.OperatorGroup:
		ref.Kind = v1.OperatorGroupKind
		ref.APIVersion = v1.SchemeGroupVersion.String()
	default:
		return nil, references.NewCannotReferenceError(objMeta, "resource not a valid olm kind")
	}

	return ref, nil
}
