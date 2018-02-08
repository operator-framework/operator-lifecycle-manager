package ownerutil

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Owner is used to build an OwnerReference, and we need type and object metadata
type Owner interface {
	metav1.Object
	schema.ObjectKind
}

func IsOwnedBy(object metav1.Object, owner Owner) bool {
	for _, oref := range object.GetOwnerReferences() {
		if oref.UID == owner.GetUID() {
			return true
		}
	}
	return false
}

func AddNonBlockingOwner(object metav1.Object, owner Owner) {
	blockOwnerDeletion := false
	isController := false

	ownerRefs := object.GetOwnerReferences()
	ownerRefs = append(ownerRefs, metav1.OwnerReference{
		APIVersion:         owner.GroupVersionKind().GroupVersion().String(),
		Kind:               owner.GroupVersionKind().Kind,
		Name:               owner.GetName(),
		UID:                owner.GetUID(),
		BlockOwnerDeletion: &blockOwnerDeletion,
		Controller:         &isController,
	})
	object.SetOwnerReferences(ownerRefs)
}
