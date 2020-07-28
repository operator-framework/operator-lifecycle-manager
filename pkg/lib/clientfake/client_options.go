package clientfake

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	clitesting "k8s.io/client-go/testing"
)

// Option configures a ClientsetDecorator
type Option func(ClientsetDecorator)

// WithSelfLinks returns a fakeClientOption that configures a ClientsetDecorator to write selfLinks to all OLM types on create.
func WithSelfLinks(tb testing.TB) Option {
	return func(c ClientsetDecorator) {
		c.PrependReactor("create", "*", func(a clitesting.Action) (bool, runtime.Object, error) {
			ca, ok := a.(clitesting.CreateAction)
			if !ok {
				tb.Fatalf("expected CreateAction")
			}

			obj := ca.GetObject()
			accessor, err := meta.Accessor(obj)
			if err != nil {
				return false, nil, err
			}
			if accessor.GetSelfLink() != "" {
				// SelfLink is already set
				return false, nil, nil
			}

			gvr := ca.GetResource()
			accessor.SetSelfLink(BuildSelfLink(gvr.GroupVersion().String(), gvr.Resource, accessor.GetNamespace(), accessor.GetName()))

			return false, obj, nil
		})
	}
}

// WithNameGeneration returns a fakeK8sClientOption that configures a Clientset to write generated names to all types on create.
func WithNameGeneration(tb testing.TB) Option {
	return func(c ClientsetDecorator) {
		c.PrependReactor("create", "*", func(a clitesting.Action) (bool, runtime.Object, error) {
			ca, ok := a.(clitesting.CreateAction)
			if !ok {
				tb.Fatalf("expected CreateAction")
			}

			return false, AddSimpleGeneratedName(ca.GetObject()), nil
		})
	}
}

var generateCall int

// WithNameGenerationList returns a fakeK8sClientOption that configures a Clientset to write generated names to all types on create.
// Names are selected from the given list sequentially in call order and cycles when exhausted.
func WithNameGenerationList(tb testing.TB, names ...string) Option {
	if len(names) < 1 {
		panic("must specify at least one generate name")
	}

	return func(c ClientsetDecorator) {
		c.PrependReactor("create", "*", func(a clitesting.Action) (bool, runtime.Object, error) {
			ca, ok := a.(clitesting.CreateAction)
			if !ok {
				tb.Fatalf("expected CreateAction")
			}

			return false, AddNextNameInList(ca.GetObject(), names), nil
		})
	}
}
