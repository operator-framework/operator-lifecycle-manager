package operatorclient

import (
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

var (
	deploymentsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	wildcard       = clienttesting.ActionImpl{Verb: "wildcard!"}
)

func testDeployment(name, namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "main",
						Image: "test:v1",
					}},
				},
			},
		},
	}
}

func TestGetDeployment(t *testing.T) {
	t.Run("deployment exists", func(t *testing.T) {
		require := require.New(t)

		existing := testDeployment("test-dep", "test-ns")
		kube := fake.NewSimpleClientset(existing)
		c := &Client{Interface: kube}

		result, err := c.GetDeployment("test-ns", "test-dep")

		require.NoError(err)
		require.Equal("test-dep", result.Name)
		require.Equal("test-ns", result.Namespace)
	})

	t.Run("deployment not found", func(t *testing.T) {
		require := require.New(t)

		kube := fake.NewSimpleClientset()
		c := &Client{Interface: kube}

		_, err := c.GetDeployment("test-ns", "nonexistent")

		require.Error(err)
		require.True(apierrors.IsNotFound(err))
	})
}

func TestCreateDeployment(t *testing.T) {
	for _, tc := range []struct {
		Name            string
		Existing        *appsv1.Deployment
		ToCreate        *appsv1.Deployment
		ExpectedActions []clienttesting.Action
		ExpectedError   bool
	}{
		{
			Name:     "deployment doesn't exist - creates successfully",
			Existing: nil,
			ToCreate: testDeployment("new-dep", "test-ns"),
			ExpectedActions: []clienttesting.Action{
				clienttesting.NewCreateAction(deploymentsGVR, "test-ns", testDeployment("new-dep", "test-ns")),
			},
			ExpectedError: false,
		},
		{
			Name:     "deployment already exists - falls back to update",
			Existing: testDeployment("existing-dep", "test-ns"),
			ToCreate: func() *appsv1.Deployment {
				dep := testDeployment("existing-dep", "test-ns")
				dep.Spec.Replicas = ptr.To[int32](2)
				return dep
			}(),
			ExpectedActions: []clienttesting.Action{
				wildcard, // Create action
				wildcard, // Get action from UpdateDeployment
				wildcard, // Patch action from PatchDeployment
			},
			ExpectedError: false,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			require := require.New(t)

			var kube *fake.Clientset
			if tc.Existing != nil {
				kube = fake.NewSimpleClientset(tc.Existing)
			} else {
				kube = fake.NewSimpleClientset()
			}
			c := &Client{Interface: kube}

			_, err := c.CreateDeployment(tc.ToCreate)

			if tc.ExpectedError {
				require.Error(err)
			} else {
				require.NoError(err)

				actual := kube.Actions()
				require.Len(actual, len(tc.ExpectedActions))
			}
		})
	}
}

func TestDeleteDeployment(t *testing.T) {
	t.Run("deployment deleted successfully", func(t *testing.T) {
		require := require.New(t)

		existing := testDeployment("test-dep", "test-ns")
		kube := fake.NewSimpleClientset(existing)
		c := &Client{Interface: kube}

		deleteOptions := &metav1.DeleteOptions{}
		err := c.DeleteDeployment("test-ns", "test-dep", deleteOptions)

		require.NoError(err)

		actions := kube.Actions()
		require.Len(actions, 1)
		deleteAction := actions[0].(clienttesting.DeleteAction)
		require.Equal(deploymentsGVR, deleteAction.GetResource())
		require.Equal("test-ns", deleteAction.GetNamespace())
		require.Equal("test-dep", deleteAction.GetName())
	})
}

func TestPatchDeployment(t *testing.T) {
	for _, tc := range []struct {
		Name              string
		Existing          *appsv1.Deployment
		Original          *appsv1.Deployment
		Modified          *appsv1.Deployment
		ExpectedChanged   bool
		ExpectedError     bool
		ExpectedErrorMsg  string
		ExpectedActions   []clienttesting.Action
		ModifyResourceVer func(*appsv1.Deployment)
	}{
		{
			Name:     "nil original - uses current as original (2-way merge)",
			Existing: testDeployment("test-dep", "test-ns"),
			Original: nil,
			Modified: func() *appsv1.Deployment {
				dep := testDeployment("test-dep", "test-ns")
				dep.Spec.Replicas = ptr.To[int32](3)
				return dep
			}(),
			ExpectedChanged: true,
			ExpectedError:   false,
			ExpectedActions: []clienttesting.Action{
				wildcard, // Get
				wildcard, // Patch
			},
			ModifyResourceVer: func(d *appsv1.Deployment) {
				d.ResourceVersion = "2" // Simulate resource version change
			},
		},
		{
			Name:             "nil modified - returns error without panic",
			Existing:         testDeployment("test-dep", "test-ns"),
			Original:         testDeployment("test-dep", "test-ns"),
			Modified:         nil,
			ExpectedChanged:  false,
			ExpectedError:    true,
			ExpectedErrorMsg: "modified cannot be nil",
		},
		{
			Name:     "no changes - empty patch, resourceVersion unchanged",
			Existing: testDeployment("test-dep", "test-ns"),
			Original: testDeployment("test-dep", "test-ns"),
			Modified: testDeployment("test-dep", "test-ns"),
			ExpectedChanged: false,
			ExpectedError:   false,
			ExpectedActions: []clienttesting.Action{
				wildcard, // Get
				wildcard, // Patch
			},
		},
		{
			Name:     "spec changes - generates patch, resourceVersion changes",
			Existing: testDeployment("test-dep", "test-ns"),
			Original: testDeployment("test-dep", "test-ns"),
			Modified: func() *appsv1.Deployment {
				dep := testDeployment("test-dep", "test-ns")
				dep.Spec.Replicas = ptr.To[int32](5)
				return dep
			}(),
			ExpectedChanged: true,
			ExpectedError:   false,
			ExpectedActions: []clienttesting.Action{
				wildcard, // Get
				wildcard, // Patch
			},
			ModifyResourceVer: func(d *appsv1.Deployment) {
				d.ResourceVersion = "2"
			},
		},
		{
			Name:     "TypeMeta differs - normalizes before patching",
			Existing: testDeployment("test-dep", "test-ns"),
			Original: testDeployment("test-dep", "test-ns"),
			Modified: func() *appsv1.Deployment {
				dep := testDeployment("test-dep", "test-ns")
				dep.TypeMeta = metav1.TypeMeta{
					Kind:       "Deployment",
					APIVersion: "apps/v1",
				}
				dep.Spec.Replicas = ptr.To[int32](2)
				return dep
			}(),
			ExpectedChanged: true,
			ExpectedError:   false,
			ExpectedActions: []clienttesting.Action{
				wildcard, // Get
				wildcard, // Patch
			},
			ModifyResourceVer: func(d *appsv1.Deployment) {
				d.ResourceVersion = "2"
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			require := require.New(t)

			var kube *fake.Clientset
			if tc.Existing != nil {
				kube = fake.NewSimpleClientset(tc.Existing)
				if tc.ModifyResourceVer != nil {
					kube.PrependReactor("patch", "deployments", func(action clienttesting.Action) (handled bool, ret runtime.Object, err error) {
						dep := tc.Existing.DeepCopy()
						tc.ModifyResourceVer(dep)
						return true, dep, nil
					})
				}
			} else {
				kube = fake.NewSimpleClientset()
			}
			c := &Client{Interface: kube}

			_, changed, err := c.PatchDeployment(tc.Original, tc.Modified)

			if tc.ExpectedError {
				require.Error(err)
				if tc.ExpectedErrorMsg != "" {
					require.Contains(err.Error(), tc.ExpectedErrorMsg)
				}
				require.False(changed)
			} else {
				require.NoError(err)
				require.Equal(tc.ExpectedChanged, changed)

				if tc.ExpectedActions != nil {
					actual := kube.Actions()
					require.Len(actual, len(tc.ExpectedActions))
				}
			}
		})
	}
}

func TestUpdateDeployment(t *testing.T) {
	for _, tc := range []struct {
		Name            string
		Existing        *appsv1.Deployment
		ToUpdate        *appsv1.Deployment
		ExpectedChanged bool
	}{
		{
			Name:     "no changes - resourceVersion unchanged",
			Existing: testDeployment("test-dep", "test-ns"),
			ToUpdate: testDeployment("test-dep", "test-ns"),
			ExpectedChanged: false,
		},
		{
			Name:     "spec modified - resourceVersion changes",
			Existing: testDeployment("test-dep", "test-ns"),
			ToUpdate: func() *appsv1.Deployment {
				dep := testDeployment("test-dep", "test-ns")
				dep.Spec.Replicas = ptr.To[int32](4)
				return dep
			}(),
			ExpectedChanged: true,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			require := require.New(t)

			kube := fake.NewSimpleClientset(tc.Existing)
			if tc.ExpectedChanged {
				kube.PrependReactor("patch", "deployments", func(action clienttesting.Action) (handled bool, ret runtime.Object, err error) {
					dep := tc.Existing.DeepCopy()
					dep.ResourceVersion = "2"
					dep.Spec = tc.ToUpdate.Spec
					return true, dep, nil
				})
			}
			c := &Client{Interface: kube}

			_, changed, err := c.UpdateDeployment(tc.ToUpdate)

			require.NoError(err)
			require.Equal(tc.ExpectedChanged, changed)
		})
	}
}

func TestCreateOrRollingUpdateDeployment(t *testing.T) {
	for _, tc := range []struct {
		Name            string
		Existing        *appsv1.Deployment
		ToApply         *appsv1.Deployment
		ExpectedCreated bool
		ExpectedError   bool
	}{
		{
			Name:            "deployment doesn't exist - creates it",
			Existing:        nil,
			ToApply:         testDeployment("new-dep", "test-ns"),
			ExpectedCreated: true,
			ExpectedError:   false,
		},
		{
			Name:     "deployment exists with changes - performs rolling update",
			Existing: testDeployment("test-dep", "test-ns"),
			ToApply: func() *appsv1.Deployment {
				dep := testDeployment("test-dep", "test-ns")
				dep.Spec.Replicas = ptr.To[int32](3)
				return dep
			}(),
			ExpectedCreated: true,
			ExpectedError:   false,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			require := require.New(t)

			var kube *fake.Clientset
			if tc.Existing != nil {
				kube = fake.NewSimpleClientset(tc.Existing)
			} else {
				kube = fake.NewSimpleClientset()
			}

			if tc.Existing != nil && tc.ToApply.Spec.Replicas != tc.Existing.Spec.Replicas {
				kube.PrependReactor("patch", "deployments", func(action clienttesting.Action) (handled bool, ret runtime.Object, err error) {
					dep := tc.Existing.DeepCopy()
					dep.ResourceVersion = "2"
					dep.Spec = tc.ToApply.Spec
					return true, dep, nil
				})
			}

			c := &Client{Interface: kube}

			_, created, err := c.CreateOrRollingUpdateDeployment(tc.ToApply)

			if tc.ExpectedError {
				require.Error(err)
			} else {
				require.NoError(err)
				require.Equal(tc.ExpectedCreated, created)
			}
		})
	}
}

func TestListDeploymentsWithLabels(t *testing.T) {
	dep1 := testDeployment("dep1", "test-ns")
	dep1.Labels = map[string]string{"app": "test", "env": "prod"}

	dep2 := testDeployment("dep2", "test-ns")
	dep2.Labels = map[string]string{"app": "test", "env": "dev"}

	dep3 := testDeployment("dep3", "other-ns")
	dep3.Labels = map[string]string{"app": "test"}

	for _, tc := range []struct {
		Name          string
		Existing      []runtime.Object
		Namespace     string
		LabelSelector labels.Set
		ExpectedCount int
	}{
		{
			Name:          "list with label selector - finds matching deployments",
			Existing:      []runtime.Object{dep1, dep2, dep3},
			Namespace:     "test-ns",
			LabelSelector: labels.Set{"app": "test"},
			ExpectedCount: 2, // dep1 and dep2 in test-ns
		},
		{
			Name:          "empty list - no matching deployments",
			Existing:      []runtime.Object{dep1, dep2},
			Namespace:     "test-ns",
			LabelSelector: labels.Set{"nonexistent": "label"},
			ExpectedCount: 0,
		},
		{
			Name:          "specific label selector",
			Existing:      []runtime.Object{dep1, dep2},
			Namespace:     "test-ns",
			LabelSelector: labels.Set{"env": "prod"},
			ExpectedCount: 1, // only dep1
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			require := require.New(t)

			kube := fake.NewSimpleClientset(tc.Existing...)
			c := &Client{Interface: kube}

			result, err := c.ListDeploymentsWithLabels(tc.Namespace, tc.LabelSelector)

			require.NoError(err)
			require.Equal(tc.ExpectedCount, len(result.Items))

			actions := kube.Actions()
			require.Greater(len(actions), 0)
			listAction := actions[0].(clienttesting.ListAction)
			require.Equal(tc.Namespace, listAction.GetNamespace())
		})
	}
}
