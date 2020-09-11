package client

import (
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/testobj"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestSetDefaultGroupVersionKind(t *testing.T) {
	tests := []struct {
		name          string
		schemebuilder runtime.SchemeBuilder
		obj           Object
		want          schema.GroupVersionKind
	}{
		{
			name:          "DefaultGVK",
			schemebuilder: corev1.SchemeBuilder,
			obj:           &corev1.ServiceAccount{},
			want: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "ServiceAccount",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := runtime.NewScheme()
			err := tt.schemebuilder.AddToScheme(s)
			require.NoError(t, err)

			SetDefaultGroupVersionKind(tt.obj, s)
			require.EqualValues(t, tt.want, tt.obj.GetObjectKind().GroupVersionKind())
		})
	}
}

func TestServerSideApply(t *testing.T) {
	tests := []struct {
		name          string
		obj           Object
		schemebuilder *runtime.SchemeBuilder
		changeFunc    interface{}
		want          Object
	}{
		{
			name: "ServerSideMetadataApply",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testpod",
					Namespace: "testns",
					Labels: map[string]string{
						"testlabel": "oldvalue",
					},
				},
			},
			schemebuilder: &corev1.SchemeBuilder,
			changeFunc: func(p *corev1.Pod) error {
				p.Labels["testlabel"] = "newvalue"
				return nil
			},
			want: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Pod",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testpod",
					Namespace: "testns",
					Labels: map[string]string{
						"testlabel": "newvalue",
					},
				},
			},
		},
		{
			name: "ServerSideStatusApply",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testpod",
					Namespace: "testns",
				},
				Status: corev1.PodStatus{Message: "new"},
			},
			schemebuilder: &corev1.SchemeBuilder,
			changeFunc: func(p *corev1.Pod) error {
				p.Status.Message = "new"
				return nil
			},
			want: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Pod",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testpod",
					Namespace: "testns",
				},
				Status: corev1.PodStatus{Message: "new"},
			},
		},
		{
			name: "ServerSideUnstructuredApply",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind":       "Pod",
					"apiVersion": "v1",
					"metadata": map[string]interface{}{
						"name":      "testpod",
						"namespace": "testns",
					},
					"status": map[string]interface{}{
						"message": "old",
					},
				},
			},
			schemebuilder: &corev1.SchemeBuilder,
			changeFunc: func(u *unstructured.Unstructured) error {
				return unstructured.SetNestedField(u.Object, "new", "status", "message")
			},
			want: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Pod",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testpod",
					Namespace: "testns",
				},
				Status: corev1.PodStatus{Message: "new"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := runtime.NewScheme()
			require.NoError(t, tt.schemebuilder.AddToScheme(s))

			applier := NewFakeApplier(s, "testowner", tt.obj)
			require.NoError(t, applier.Apply(context.TODO(), tt.obj, tt.changeFunc)())

			newObj := corev1.Pod{}
			require.NoError(t, applier.client.Get(context.TODO(), testobj.NamespacedName(tt.obj), &newObj))

			newObj.ResourceVersion = ""
			require.EqualValues(t, tt.want, &newObj)
		})
	}
}
