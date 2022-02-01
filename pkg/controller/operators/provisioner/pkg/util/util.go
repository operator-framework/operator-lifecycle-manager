package util

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func PodName(bundleName string) string {
	return fmt.Sprintf("kuberpak-unpack-bundle-%s", bundleName)
}

func BundleLabels(bundleName string) map[string]string {
	return map[string]string{"kuberpak.io/bundle-name": bundleName}
}

func MetadataConfigMapName(bundleName string) string {
	return fmt.Sprintf("bundle-metadata-%s", bundleName)
}

func CreateOrRecreate(ctx context.Context, cl client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	key := client.ObjectKeyFromObject(obj)
	if err := cl.Get(ctx, key, obj); err != nil {
		if !apierrors.IsNotFound(err) {
			return controllerutil.OperationResultNone, err
		}
		if err := mutate(f, key, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if err := cl.Create(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultCreated, nil
	}

	existing := obj.DeepCopyObject() //nolint
	if err := mutate(f, key, obj); err != nil {
		return controllerutil.OperationResultNone, err
	}

	if equality.Semantic.DeepEqual(existing, obj) {
		return controllerutil.OperationResultNone, nil
	}

	if err := wait.PollImmediateUntil(time.Millisecond*5, func() (done bool, err error) {
		if err := cl.Delete(ctx, obj); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	}, ctx.Done()); err != nil {
		return controllerutil.OperationResultNone, err
	}

	obj.SetUID("")
	obj.SetResourceVersion("")
	obj.SetGeneration(0)
	if err := cl.Create(ctx, obj); err != nil {
		return controllerutil.OperationResultNone, err
	}
	return controllerutil.OperationResultUpdated, nil
}

// mutate wraps a MutateFn and applies validation to its result.
func mutate(f controllerutil.MutateFn, key client.ObjectKey, obj client.Object) error {
	if err := f(); err != nil {
		return err
	}
	if newKey := client.ObjectKeyFromObject(obj); key != newKey {
		return fmt.Errorf("MutateFn cannot mutate object name and/or object namespace")
	}
	return nil
}

func MergeMaps(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func ConfigMapsEqual(a, b corev1.ConfigMap) bool {
	//if !stringMapsEqual(a.Labels, b.Labels) {
	//	fmt.Println("labels differ", a.Labels, b.Labels)
	//}
	//if !stringMapsEqual(a.Annotations, b.Annotations) {
	//	fmt.Println("annotations differ", a.Annotations, b.Annotations)
	//}
	//if !ownerRefsEqual(a.OwnerReferences, b.OwnerReferences) {
	//	fmt.Println("ownerrefs differ", a.OwnerReferences, b.OwnerReferences)
	//}
	//if !stringMapsEqual(a.Data, b.Data) {
	//	fmt.Println("data differs", a.Data, b.Data)
	//}
	//if !bytesMapsEqual(a.BinaryData, b.BinaryData) {
	//	fmt.Println("binary data differs", a.BinaryData, b.BinaryData)
	//}

	return stringMapsEqual(a.Labels, b.Labels) &&
		stringMapsEqual(a.Annotations, b.Annotations) &&
		ownerRefsEqual(a.OwnerReferences, b.OwnerReferences) &&
		stringMapsEqual(a.Data, b.Data) &&
		bytesMapsEqual(a.BinaryData, b.BinaryData)
}

func stringMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for ka, va := range a {
		vb, ok := b[ka]
		if !ok || va != vb {
			return false
		}
	}
	return true
}

func bytesMapsEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for ka, va := range a {
		vb, ok := b[ka]
		if !ok || !bytes.Equal(va, vb) {
			return false
		}
	}
	return true
}

func ownerRefsEqual(a, b []metav1.OwnerReference) bool {
	if len(a) != len(b) {
		return false
	}
	for i, ora := range a {
		orb := b[i]
		if !reflect.DeepEqual(ora, orb) {
			return false
		}
	}
	return true
}
