package reference

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators"
	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

func TestGetReference(t *testing.T) {
	type args struct {
		obj runtime.Object
	}
	type want struct {
		ref *corev1.ObjectReference
		err error
	}
	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "Nil/Error",
			args: args{obj: nil},
			want: want{
				ref: nil,
				err: fmt.Errorf("can't reference a nil object"),
			},
		},
		{
			name: "v1/Pod/NotRegistered/Error",
			args: args{&corev1.Pod{}},
			want: want{
				ref: nil,
				err: runtime.NewNotRegisteredErrForType(scheme.Name(), reflect.TypeOf(corev1.Pod{})),
			},
		},
		{
			name: "v1alpha1/ClusterServiceVersion",
			args: args{
				&v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "csv",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(v1alpha1.SchemeGroupVersion.String(), "clusterserviceversions", "ns", "csv"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "csv",
					UID:        types.UID("uid"),
					Kind:       v1alpha1.ClusterServiceVersionKind,
					APIVersion: v1alpha1.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
		{
			name: "v1alpha1/InstallPlan",
			args: args{
				&v1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "ip",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(v1alpha1.SchemeGroupVersion.String(), "installplans", "ns", "ip"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "ip",
					UID:        types.UID("uid"),
					Kind:       v1alpha1.InstallPlanKind,
					APIVersion: v1alpha1.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
		{
			name: "v1alpha1/Subscription",
			args: args{
				&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "sub",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(v1alpha1.SchemeGroupVersion.String(), "subscriptions", "ns", "sub"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "sub",
					UID:        types.UID("uid"),
					Kind:       v1alpha1.SubscriptionKind,
					APIVersion: v1alpha1.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
		{
			name: "v1alpha1/CatalogSource",
			args: args{
				&v1alpha1.CatalogSource{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "catsrc",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(v1alpha1.SchemeGroupVersion.String(), "catalogsources", "ns", "catsrc"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "catsrc",
					UID:        types.UID("uid"),
					Kind:       v1alpha1.CatalogSourceKind,
					APIVersion: v1alpha1.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
		{
			name: "v1/OperatorGroup",
			args: args{
				&v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "og",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(v1.SchemeGroupVersion.String(), "operatorgroups", "ns", "og"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "og",
					UID:        types.UID("uid"),
					Kind:       v1.OperatorGroupKind,
					APIVersion: v1.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
		{
			name: "internalversion/ClusterServiceVersion",
			args: args{
				&operators.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "csv",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(operators.SchemeGroupVersion.String(), "clusterserviceversions", "ns", "csv"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "csv",
					UID:        types.UID("uid"),
					Kind:       operators.ClusterServiceVersionKind,
					APIVersion: operators.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
		{
			name: "internalversion/InstallPlan",
			args: args{
				&v1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "ip",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(operators.SchemeGroupVersion.String(), "installplans", "ns", "ip"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "ip",
					UID:        types.UID("uid"),
					Kind:       operators.InstallPlanKind,
					APIVersion: operators.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
		{
			name: "internalversion/Subscription",
			args: args{
				&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "sub",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(operators.SchemeGroupVersion.String(), "subscriptions", "ns", "sub"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "sub",
					UID:        types.UID("uid"),
					Kind:       operators.SubscriptionKind,
					APIVersion: operators.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
		{
			name: "internalversion/CatalogSource",
			args: args{
				&v1alpha1.CatalogSource{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "catsrc",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(operators.SchemeGroupVersion.String(), "catalogsources", "ns", "catsrc"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "catsrc",
					UID:        types.UID("uid"),
					Kind:       operators.CatalogSourceKind,
					APIVersion: operators.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
		{
			name: "internalversion/OperatorGroup",
			args: args{
				&v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "og",
						UID:       types.UID("uid"),
						SelfLink:  buildSelfLink(operators.SchemeGroupVersion.String(), "operatorgroups", "ns", "og"),
					},
				},
			},
			want: want{
				ref: &corev1.ObjectReference{
					Namespace:  "ns",
					Name:       "og",
					UID:        types.UID("uid"),
					Kind:       operators.OperatorGroupKind,
					APIVersion: operators.SchemeGroupVersion.String(),
				},
				err: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := GetReference(tt.args.obj)
			require.Equal(t, tt.want.err, err)
			require.Equal(t, tt.want.ref, ref)
		})
	}
}

// buildSelfLink returns a selfLink.
func buildSelfLink(groupVersion, plural, namespace, name string) string {
	if namespace == metav1.NamespaceAll {
		return fmt.Sprintf("/apis/%s/%s/%s", groupVersion, plural, name)
	}
	return fmt.Sprintf("/apis/%s/namespaces/%s/%s/%s", groupVersion, namespace, plural, name)
}
