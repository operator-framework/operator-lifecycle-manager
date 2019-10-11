package testobj

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/reference"
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

// WithLabel sets the given key/value pair on the labels of each object given and panics if it can't access the object's meta.
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

// GetReferences gets ObjectReferences for the given objects and panics if it can't generate a reference.
func GetReferences(scheme *runtime.Scheme, objs ...runtime.Object) (refs []*corev1.ObjectReference) {
	for _, obj := range objs {
		refs = append(refs, GetReference(scheme, obj))
	}

	return
}

// GetReference gets an ObjectReference for the given object and panics if it can't access the object's meta.
func GetReference(scheme *runtime.Scheme, obj runtime.Object) *corev1.ObjectReference {
	ref, err := reference.GetReference(scheme, obj)
	if err != nil {
		panic(fmt.Errorf("error creating resource reference: %v", err))
	}

	return ref
}

// WithItems sets the items of the list given and panics if it can't.
func WithItems(list runtime.Object, items ...runtime.Object) runtime.Object {
	out := list.DeepCopyObject()
	if err := meta.SetList(out, items); err != nil {
		panic(fmt.Sprintf("error setting list elements: %v", err))
	}

	return out
}
