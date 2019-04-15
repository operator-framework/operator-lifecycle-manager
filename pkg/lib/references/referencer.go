package references

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// CannotReferenceError indicates that an ObjectReference could not be generated for a resource.
type CannotReferenceError struct {
	obj interface{}
	msg string
}

// Error returns the error's error string.
func (err *CannotReferenceError) Error() string {
	return fmt.Sprintf("cannot reference object %v: %s", err.obj, err.msg)
}

// NewCannotReferenceError returns a pointer to a CannotReferenceError instantiated with the given object and message.
func NewCannotReferenceError(obj interface{}, msg string) *CannotReferenceError {
	return &CannotReferenceError{obj: obj, msg: msg}
}

// ObjectReferencer knows how to return an ObjectReference for a resource.
type ObjectReferencer interface {
	// ObjectReferenceFor returns an ObjectReference for the given resource.
	ObjectReferenceFor(obj interface{}) (*corev1.ObjectReference, error)
}

// ObjectReferencerFunc is a function type that implements ObjectReferencer.
type ObjectReferencerFunc func(obj interface{}) (*corev1.ObjectReference, error)

// ObjectReferenceFor returns an ObjectReference for the current resource by invoking itself.
func (f ObjectReferencerFunc) ObjectReferenceFor(obj interface{}) (*corev1.ObjectReference, error) {
	return f(obj)
}
