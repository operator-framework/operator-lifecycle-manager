package testobj

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// WithName sets the name of the given object and panics if it can't access the object's meta.
func WithName(name string, obj runtime.Object) runtime.Object {
	out := obj.DeepCopyObject()
	m, err := meta.Accessor(out)
	if err != nil {
		panic(fmt.Sprintf("error setting name: %v", err))
	}

	m.SetName(name)

	return out
}

// WithNamespace sets the namespace of the given object and panics if it can't access the object's meta.
func WithNamespace(namespace string, obj runtime.Object) RuntimeMetaObject {
	out := obj.DeepCopyObject().(RuntimeMetaObject)
	out.SetNamespace(namespace)

	return out
}

// WithNamespacedName sets the namespace and name of the given object.
func WithNamespacedName(name *types.NamespacedName, obj runtime.Object) RuntimeMetaObject {
	out := obj.DeepCopyObject().(RuntimeMetaObject)
	out.SetNamespace(name.Namespace)
	out.SetName(name.Name)

	return out
}

// NamespacedName returns the namespaced name of the given object and panics if it can't access the object's meta.
func NamespacedName(obj runtime.Object) types.NamespacedName {
	m, err := meta.Accessor(obj)
	if err != nil {
		panic(fmt.Sprintf("error setting namespaced name: %v", err))
	}

	return types.NamespacedName{Namespace: m.GetNamespace(), Name: m.GetName()}
}

// WithLabel sets the given key/value pair on the labels of each object given and panics if it can't access an object's meta.
func WithLabel(key, value string, objs ...runtime.Object) (labelled []runtime.Object) {
	for _, obj := range objs {
		out := obj.DeepCopyObject()
		m, err := meta.Accessor(out)
		if err != nil {
			panic(fmt.Sprintf("error setting label: %v", err))
		}

		if len(m.GetLabels()) < 1 {
			m.SetLabels(map[string]string{})
		}
		m.GetLabels()[key] = value

		labelled = append(labelled, out)
	}

	return
}

// WithLabels sets the labels of an object and returns the updated result.
func WithLabels(labels map[string]string, obj RuntimeMetaObject) RuntimeMetaObject {
	out := obj.DeepCopyObject().(RuntimeMetaObject)
	out.SetLabels(labels)
	return out
}

// StripLabel removes the label with the given key from each object given and panics if it can't access an object's meta.
func StripLabel(key string, objs ...runtime.Object) (stripped []runtime.Object) {
	for _, obj := range objs {
		out := obj.DeepCopyObject()
		m, err := meta.Accessor(out)
		if err != nil {
			panic(fmt.Sprintf("error setting label: %v", err))
		}

		delete(m.GetLabels(), key)

		stripped = append(stripped, out)
	}

	return
}

// GetUnstructured gets an Unstructured for the given object and panics if it fails.
func GetUnstructured(scheme *runtime.Scheme, obj runtime.Object) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	if err := scheme.Convert(obj, u, nil); err != nil {
		panic(fmt.Errorf("error creating unstructured: %v", err))
	}

	return u
}

// WithItems sets the items of the list given and panics if it fails.
func WithItems(list runtime.Object, items ...runtime.Object) runtime.Object {
	out := list.DeepCopyObject()
	if err := meta.SetList(out, items); err != nil {
		panic(fmt.Sprintf("error setting list elements: %v", err))
	}

	return out
}

// MarshalJSON marshals an object to JSON and panics if it can't.
func MarshalJSON(obj runtime.Object) (marshaled []byte) {
	var err error
	if marshaled, err = json.Marshal(obj); err != nil {
		panic(fmt.Sprintf("failed to marshal obj to json: %s", err))
	}

	return
}

var (
	notController          = false
	dontBlockOwnerDeletion = false
)

// WithOwner appends the an owner to an object and returns a panic if it fails.
func WithOwner(owner, obj RuntimeMetaObject) RuntimeMetaObject {
	out := obj.DeepCopyObject().(RuntimeMetaObject)
	gvk := owner.GetObjectKind().GroupVersionKind()
	apiVersion, kind := gvk.ToAPIVersionAndKind()
	refs := append(out.GetOwnerReferences(), metav1.OwnerReference{
		APIVersion:         apiVersion,
		Kind:               kind,
		Name:               owner.GetName(),
		UID:                owner.GetUID(),
		BlockOwnerDeletion: &dontBlockOwnerDeletion,
		Controller:         &notController,
	})
	out.SetOwnerReferences(refs)

	return out
}
