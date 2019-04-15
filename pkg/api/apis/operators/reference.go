package operators

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/reference"
)

// GetReference returns an ObjectReference for a given object whose concrete value is an OLM type.
func GetReference(obj runtime.Object) (*corev1.ObjectReference, error) {
	return reference.GetReference(scheme, obj)
}
