package ownerutil

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis"
	csv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/catalogsource/v1alpha1"
	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
	ipv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	subv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/subscription/v1alpha1"
	log "github.com/sirupsen/logrus"
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

// AddNonBlockingOwner adds a nonblocking owner to the ownerref list.
func AddNonBlockingOwner(object metav1.Object, owner Owner) {
	// Most of the time we won't have TypeMeta on the object, so we infer it for types we know about
	inferGroupVersionKind(owner)
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

// inferGroupVersionKind adds TypeMeta to an owner so that it can be written to an ownerref.
// TypeMeta is generally only known at serialization time, so we often won't know what GVK an owner has.
// For the types we know about, we can add the GVK of the apis that we're using the interact with the object.
func inferGroupVersionKind(owner Owner) {
	if !owner.GroupVersionKind().Empty() {
		// owner already has TypeMeta, no inference needed
		return
	}

	switch v := owner.(type) {
	case *csvv1alpha1.ClusterServiceVersion:
		owner.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   apis.GroupName,
			Version: csvv1alpha1.GroupVersion,
			Kind:    csvv1alpha1.ClusterServiceVersionKind,
		})
	case *ipv1alpha1.InstallPlan:
		owner.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   apis.GroupName,
			Version: ipv1alpha1.GroupVersion,
			Kind:    ipv1alpha1.InstallPlanKind,
		})
	case *subv1alpha1.Subscription:
		owner.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   apis.GroupName,
			Version: subv1alpha1.GroupVersion,
			Kind:    subv1alpha1.SubscriptionKind,
		})
	case *csv1alpha1.CatalogSource:
		owner.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   apis.GroupName,
			Version: csv1alpha1.GroupVersion,
			Kind:    csv1alpha1.CatalogSourceKind,
		})
	default:
		log.Warnf("could not infer GVK for object: %#v, %#v", v, owner)
	}
	return
}
