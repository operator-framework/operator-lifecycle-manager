package ownerutil

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
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
	// TODO: Remove as soon as possible
	// This is a hack, for some reason CSVs that we get out of the informer are missing
	// TypeMeta, which means we can't get the APIVersion or Kind generically here.
	// The underlying issue should be found and fixes as soon as possible
	// This needs to be removed before a new APIVersion is cut
	if _, ok := owner.(*v1alpha1.ClusterServiceVersion); ok {
		owner.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   apis.GroupName,
			Version: v1alpha1.GroupVersion,
			Kind:    v1alpha1.ClusterServiceVersionKind,
		})
	}

	blockOwnerDeletion := false
	isController := false

	ownerRefs := object.GetOwnerReferences()
	if ownerRefs == nil {
		ownerRefs = []metav1.OwnerReference{}
	}
	gvk := owner.GroupVersionKind()
	apiVersion, kind := gvk.ToAPIVersionAndKind()
	ownerRefs = append(ownerRefs, metav1.OwnerReference{
		APIVersion:         apiVersion,
		Kind:               kind,
		Name:               owner.GetName(),
		UID:                owner.GetUID(),
		BlockOwnerDeletion: &blockOwnerDeletion,
		Controller:         &isController,
	})
	object.SetOwnerReferences(ownerRefs)
}
