package client

import (
	"context"
	"fmt"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"reflect"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultOwner = "olm.registry"
)

type Object interface {
	runtime.Object
	metav1.Object
}

func NewForConfig(c *rest.Config, scheme *runtime.Scheme, owner string) (*ServerSideApplier, error) {
	if scheme == nil {
		scheme = runtime.NewScheme()
		localSchemeBuilder := runtime.NewSchemeBuilder(
			kscheme.AddToScheme,
			apiextensionsv1.AddToScheme,
			apiregistrationv1.AddToScheme,
		)
		if err := localSchemeBuilder.AddToScheme(scheme); err != nil {
			return nil, err
		}
	}
	restClient, err := k8scontrollerclient.New(c, k8scontrollerclient.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, err
	}

	if len(owner) == 0 {
		owner = defaultOwner
	}

	return &ServerSideApplier{
		client: restClient,
		Scheme: scheme,
		Owner:  k8scontrollerclient.FieldOwner(owner),
	}, nil
}

func SetDefaultGroupVersionKind(obj Object, s *runtime.Scheme) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Empty() && s != nil {
		// Best-effort guess the GVK
		gvks, _, err := s.ObjectKinds(obj)
		if err != nil {
			panic(fmt.Sprintf("unable to get gvks for object %T: %s", obj, err))
		}
		if len(gvks) == 0 || gvks[0].Empty() {
			panic(fmt.Sprintf("unexpected gvks registered for object %T: %v", obj, gvks))
		}
		// TODO: The same object can be registered for multiple group versions
		// (although in practise this doesn't seem to be used).
		// In such case, the version set may not be correct.
		gvk = gvks[0]
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)
}

type ServerSideApplier struct {
	client k8scontrollerclient.Client
	Scheme *runtime.Scheme
	// Owner becomes the Field Manager for whatever field the Server-Side apply acts on
	Owner k8scontrollerclient.FieldOwner
}

// Apply returns a function that invokes a change func on an object and performs a server-side apply patch with the result and its status subresource.
// The given resource must be a pointer to a struct that specifies its Name, Namespace, APIVersion, and Kind at minimum.
// The given change function must be unary, matching the signature: "func(<obj type>) error".
// The returned function is suitable for use w/ asyncronous assertions.
// The underlying value of the given resource pointer is updated to reflect the latest cluster state each time the closure is successfully invoked.
// Ex. Change the spec of an existing InstallPlan
//
// plan := &InstallPlan{}
// plan.SetNamespace("ns")
// plan.SetName("install-123def")
// Eventually(c.Apply(plan, func(p *v1alpha1.InstallPlan) error {
//		p.Spec.Approved = true
//		return nil
// })).Should(Succeed())
func (c *ServerSideApplier) Apply(ctx context.Context, obj Object, changeFunc interface{}) func() error {
	// Ensure given object is a pointer
	objType := reflect.TypeOf(obj)
	if objType.Kind() != reflect.Ptr {
		panic(fmt.Sprintf("argument object must be a pointer"))
	}

	// Ensure given function matches expected signature
	var (
		change     = reflect.ValueOf(changeFunc)
		changeType = change.Type()
	)
	if n := changeType.NumIn(); n != 1 {
		panic(fmt.Sprintf("unexpected number of formal parameters in change function signature: expected 1, present %d", n))
	}
	if pt := changeType.In(0); pt.Kind() != reflect.Interface {
		if objType != pt {
			panic(fmt.Sprintf("argument object type does not match the change function parameter type: argument %s, parameter: %s", objType, pt))
		}
	} else if !objType.Implements(pt) {
		panic(fmt.Sprintf("argument object type does not implement the change function parameter type: argument %s, parameter: %s", objType, pt))
	}
	if n := changeType.NumOut(); n != 1 {
		panic(fmt.Sprintf("unexpected number of return values in change function signature: expected 1, present %d", n))
	}
	var err error
	if rt := changeType.Out(0); !rt.Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		panic(fmt.Sprintf("unexpected return type in change function signature: expected %t, present %s", err, rt))
	}

	// Determine if we need to apply a status subresource
	_, applyStatus := objType.Elem().FieldByName("Status")

	if unstructuredObj, ok := obj.(*unstructured.Unstructured); ok {
		_, applyStatus = unstructuredObj.Object["status"]
	}

	key, err := k8scontrollerclient.ObjectKeyFromObject(obj)
	if err != nil {
		panic(fmt.Sprintf("unable to extract key from resource: %s", err))
	}

	// Ensure the GVK is set before patching
	SetDefaultGroupVersionKind(obj, c.Scheme)

	return func() error {
		changed := func(obj Object) (Object, error) {
			cp := obj.DeepCopyObject().(Object)
			if err := c.client.Get(ctx, key, cp); err != nil {
				return nil, err
			}
			// Reset the GVK after the client call strips it
			SetDefaultGroupVersionKind(cp, c.Scheme)
			cp.SetManagedFields(nil)

			out := change.Call([]reflect.Value{reflect.ValueOf(cp)})
			if len(out) != 1 {
				panic(fmt.Sprintf("unexpected number of return values from apply mutation func: expected 1, returned %d", len(out)))
			}

			if err := out[0].Interface(); err != nil {
				return nil, err.(error)
			}

			return cp, nil
		}

		cp, err := changed(obj)
		if err != nil {
			return err
		}

		if len(c.Owner) == 0 {
			c.Owner = defaultOwner
		}

		if err := c.client.Patch(ctx, cp, k8scontrollerclient.Apply, k8scontrollerclient.ForceOwnership, c.Owner); err != nil {
			fmt.Printf("first patch error: %s\n", err)
			return err
		}

		if !applyStatus {
			reflect.ValueOf(obj).Elem().Set(reflect.ValueOf(cp).Elem())
			return nil
		}

		cp, err = changed(cp)
		if err != nil {
			return err
		}

		if err := c.client.Status().Patch(ctx, cp, k8scontrollerclient.Apply, k8scontrollerclient.ForceOwnership, c.Owner); err != nil {
			fmt.Printf("second patch error: %s\n", err)
			return err
		}

		reflect.ValueOf(obj).Elem().Set(reflect.ValueOf(cp).Elem())

		return nil
	}
}
