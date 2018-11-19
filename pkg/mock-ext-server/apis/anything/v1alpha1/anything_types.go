package v1alpha1

import (
	// "k8s.io/apimachinery/pkg/runtime"
	// "k8s.io/apimachinery/pkg/runtime/schema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// // AnythingKind represents any GVK
// type AnythingKind struct {
// 	gvk schema.GroupVersionKind
// }

// // SetGroupVersionKind sets the GVK
// func (k *AnythingKind) SetGroupVersionKind(kind schema.GroupVersionKind) {
// 	k.gvk = kind
// }

// // GroupVersionKind returns the stored GVK
// func (k *AnythingKind) GroupVersionKind() schema.GroupVersionKind {
// 	return k.gvk
// }

// // Anything implements the Object interface and can be used to register any GVK
// type Anything struct {
// 	kind *AnythingKind
// 	prop string `json:"prop,omitempty"`
// }

// // GetObjectKind returns the Object's GVK
// func (a *Anything) GetObjectKind() schema.ObjectKind {
// 	return a.kind
// }

// // DeepCopyObject returns a deep copy
// func (a *Anything) DeepCopyObject() runtime.Object {
// 	return NewAnything(a.GetObjectKind().GroupVersionKind())
// }

// // NewAnything returns a new instance of Anything
// func NewAnything(gvk schema.GroupVersionKind) *Anything {
// 	return &Anything{
// 		kind: &AnythingKind{
// 			gvk: gvk,
// 		},
// 	}
// }

// Anything is used as the underlying type for any mocked resource.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Anything struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}
