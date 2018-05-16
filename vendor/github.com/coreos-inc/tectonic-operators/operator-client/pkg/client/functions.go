package client

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpdateFunction defines a function that updates an object in an Update* function. The function
// provides the current instance of the object retrieved from the apiserver. The function should
// return the updated object to be applied.
type UpdateFunction func(current metav1.Object) (metav1.Object, error)

// Update returns a default UpdateFunction implementation that passes its argument through to the
// Update* function directly, ignoring the current object.
//
// Example usage:
//
// client.UpdateDaemonSet(namespace, name, types.Update(obj))
func Update(obj metav1.Object) UpdateFunction {
	return func(_ metav1.Object) (metav1.Object, error) {
		return obj, nil
	}
}

// PatchFunction defines a function that is used to provide patch objects for a 3-way merge. The
// function provides the current instance of the object retrieved from the apiserver. The function
// should return the "original" and "modified" objects (in that order) for 3-way patch computation.
type PatchFunction func(current metav1.Object) (metav1.Object, metav1.Object, error)

// Patch returns a default PatchFunction implementation that passes its arguments through to the
// patcher directly, ignoring the current object.
//
// Example usage:
//
// client.PatchDaemonSet(namespace, name, types.Patch(original, current))
func Patch(original metav1.Object, modified metav1.Object) PatchFunction {
	return func(_ metav1.Object) (metav1.Object, metav1.Object, error) {
		return original, modified, nil
	}
}

// updateToPatch wraps an UpdateFunction as a PatchFunction.
func updateToPatch(f UpdateFunction) PatchFunction {
	return func(obj metav1.Object) (metav1.Object, metav1.Object, error) {
		obj, err := f(obj)
		return nil, obj, err
	}
}
