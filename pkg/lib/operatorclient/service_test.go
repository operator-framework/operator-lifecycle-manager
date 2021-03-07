package operatorclient

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateService(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}

	// In a test expectation, matches any single fake client action.
	var wildcard clienttesting.Action = clienttesting.ActionImpl{Verb: "wildcard!"}

	for _, tc := range []struct {
		Name     string
		Old      *corev1.Service
		New      *corev1.Service
		Expected []clienttesting.Action
	}{
		{
			Name: "no changes",
			Old: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
			},
			New: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
			},
			Expected: []clienttesting.Action{
				wildcard,
				clienttesting.NewPatchAction(gvr, "namespace", "name", types.StrategicMergePatchType, []byte(`{}`)),
			},
		},
		{
			Name: "resourceversion not patched",
			Old: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name",
					Namespace:       "namespace",
					ResourceVersion: "42",
				},
			},
			New: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
			},
			Expected: []clienttesting.Action{
				wildcard,
				clienttesting.NewPatchAction(gvr, "namespace", "name", types.StrategicMergePatchType, []byte(`{}`)),
			},
		},
		{
			Name: "clusterip not patched if omitted",
			Old: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: "1.2.3.4",
				},
			},
			New: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
			},
			Expected: []clienttesting.Action{
				wildcard,
				clienttesting.NewPatchAction(gvr, "namespace", "name", types.StrategicMergePatchType, []byte(`{}`)),
			},
		},
		{
			Name: "clusterip not patched if unchanged",
			Old: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: "1.2.3.4",
				},
			},
			New: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: "1.2.3.4",
				},
			},
			Expected: []clienttesting.Action{
				wildcard,
				clienttesting.NewPatchAction(gvr, "namespace", "name", types.StrategicMergePatchType, []byte(`{}`)),
			},
		},
		{
			Name: "clusterip patched if changed", // even though the patch will be rejected due to field immutability
			Old: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: "1.2.3.4",
				},
			},
			New: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: "4.3.2.1",
				},
			},
			Expected: []clienttesting.Action{
				wildcard,
				clienttesting.NewPatchAction(gvr, "namespace", "name", types.StrategicMergePatchType, []byte(`{"spec":{"clusterIP":"4.3.2.1"}}`)),
			},
		},
		{
			Name: "spec modified",
			Old: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
				Spec: corev1.ServiceSpec{
					SessionAffinity: "None",
				},
			},
			New: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "namespace",
				},
				Spec: corev1.ServiceSpec{
					SessionAffinity: "ClientIP",
				},
			},
			Expected: []clienttesting.Action{
				wildcard,
				clienttesting.NewPatchAction(gvr, "namespace", "name", types.StrategicMergePatchType, []byte(`{"spec":{"sessionAffinity":"ClientIP"}}`)),
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			require := require.New(t)

			kube := fake.NewSimpleClientset(tc.Old)
			c := &Client{
				Interface: kube,
			}

			_, err := c.UpdateService(tc.New)
			require.NoError(err)

			actual := kube.Actions()
			require.Len(actual, len(tc.Expected))

			for i, action := range kube.Actions() {
				if tc.Expected[i] == wildcard {
					continue
				}
				require.Equal(tc.Expected[i], action)
			}
		})
	}
}
