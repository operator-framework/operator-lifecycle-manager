package operators

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/references"
)

func testObjectReferenceFor(t *testing.T, referencer references.ObjectReferencer) {
	tests := []struct {
		name string
		obj  interface{}
		ref  *corev1.ObjectReference
		err  error
	}{
		{
			name: "Nil/Error",
			obj:  nil,
			ref:  nil,
			err:  references.NewCannotReferenceError(nil, "object does not implement the Object interfaces"),
		},
		{
			name: "NotObject/Error",
			obj:  struct{ doesnt string }{doesnt: "implement object interfaces"},
			ref:  nil,
			err:  references.NewCannotReferenceError(struct{ doesnt string }{doesnt: "implement object interfaces"}, "object does not implement the Object interfaces"),
		},
		{
			name: "IncorrectKind/Error",
			obj:  &corev1.Pod{},
			ref:  nil,
			err:  references.NewCannotReferenceError(&corev1.Pod{}, "resource not a valid olm kind"),
		},
		{
			name: "ClusterServiceVersion",
			obj:  objectFor(v1alpha1.ClusterServiceVersionKind, "ns", "csv", types.UID("uid")),
			ref: &corev1.ObjectReference{
				Namespace:  "ns",
				Name:       "csv",
				UID:        types.UID("uid"),
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
			},
			err: nil,
		},
		{
			name: "InstallPlan",
			obj:  objectFor(v1alpha1.InstallPlanKind, "ns", "ip", types.UID("uid")),
			ref: &corev1.ObjectReference{
				Namespace:  "ns",
				Name:       "ip",
				UID:        types.UID("uid"),
				Kind:       v1alpha1.InstallPlanKind,
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
			},
			err: nil,
		},
		{
			name: "Subscription",
			obj:  objectFor(v1alpha1.SubscriptionKind, "ns", "sub", types.UID("uid")),
			ref: &corev1.ObjectReference{
				Namespace:  "ns",
				Name:       "sub",
				UID:        types.UID("uid"),
				Kind:       v1alpha1.SubscriptionKind,
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
			},
			err: nil,
		},
		{
			name: "CatalogSource",
			obj:  objectFor(v1alpha1.CatalogSourceKind, "ns", "catsrc", types.UID("uid")),
			ref: &corev1.ObjectReference{
				Namespace:  "ns",
				Name:       "catsrc",
				UID:        types.UID("uid"),
				Kind:       v1alpha1.CatalogSourceKind,
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
			},
			err: nil,
		},
		{
			name: "OperatorGroup",
			obj:  objectFor(v1.OperatorGroupKind, "ns", "og", types.UID("uid")),
			ref: &corev1.ObjectReference{
				Namespace:  "ns",
				Name:       "og",
				UID:        types.UID("uid"),
				Kind:       v1.OperatorGroupKind,
				APIVersion: v1.SchemeGroupVersion.String(),
			},
			err: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := referencer.ObjectReferenceFor(tt.obj)
			require.Equal(t, tt.err, err)
			require.Equal(t, tt.ref, ref)
		})
	}
}

func TestObjectReferenceFor(t *testing.T) {
	testObjectReferenceFor(t, references.ObjectReferencerFunc(ObjectReferenceFor))
}

func objectFor(kind, namespace, name string, uid types.UID) runtime.Object {
	objMeta := metav1.ObjectMeta{
		Namespace: namespace,
		Name:      name,
		UID:       uid,
	}
	switch kind {
	case v1alpha1.ClusterServiceVersionKind:
		return &v1alpha1.ClusterServiceVersion{
			ObjectMeta: objMeta,
		}
	case v1alpha1.InstallPlanKind:
		return &v1alpha1.InstallPlan{
			ObjectMeta: objMeta,
		}
	case v1alpha1.SubscriptionKind:
		return &v1alpha1.Subscription{
			ObjectMeta: objMeta,
		}
	case v1alpha1.CatalogSourceKind:
		return &v1alpha1.CatalogSource{
			ObjectMeta: objMeta,
		}
	case v1.OperatorGroupKind:
		return &v1.OperatorGroup{
			ObjectMeta: objMeta,
		}
	}

	return nil
}
