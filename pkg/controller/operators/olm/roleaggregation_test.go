package olm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/utils/ptr"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
)

const (
	testCRDName = "widgets.test.io"
	testCRDUID  = types.UID("crd-uid")
)

func roleAggregationTestCSV(name, namespace string) *v1alpha1.ClusterServiceVersion {
	return &v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(name + "-" + namespace),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{{Name: testCRDName, Version: "v1", Kind: "Widget"}},
			},
		},
		Status: v1alpha1.ClusterServiceVersionStatus{Phase: v1alpha1.CSVPhaseSucceeded},
	}
}

func roleAggregationTestCRDPartial() *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		TypeMeta:   metav1.TypeMeta{Kind: "customresourcedefinition", APIVersion: "apiextensions.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: testCRDName, UID: testCRDUID},
	}
}

func roleAggregationTestOLMConfig(disabled *bool) *operatorsv1.OLMConfig {
	return &operatorsv1.OLMConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       operatorsv1.OLMConfigSpec{Features: &operatorsv1.Features{DisableRoleAggregation: disabled}},
	}
}

// TestRoleAggregationIsEnabled covers the policy accessor: absent singleton
// and unset field fail open to legacy (enabled) behavior.
func TestRoleAggregationIsEnabled(t *testing.T) {
	table := []struct {
		name     string
		olmcfg   *operatorsv1.OLMConfig
		expected bool
	}{
		{name: "no olmconfig singleton", expected: true},
		{name: "field unset", olmcfg: roleAggregationTestOLMConfig(nil), expected: true},
		{name: "field false", olmcfg: roleAggregationTestOLMConfig(ptr.To(false)), expected: true},
		{name: "field true", olmcfg: roleAggregationTestOLMConfig(ptr.To(true)), expected: false},
	}
	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			var opts []fakeOperatorOption
			if tt.olmcfg != nil {
				opts = append(opts, withClientObjs(tt.olmcfg))
			}
			op, err := NewFakeOperator(ctx, opts...)
			require.NoError(t, err)
			enabled, err := op.roleAggregationIsEnabled()
			require.NoError(t, err)
			require.Equal(t, tt.expected, enabled)
		})
	}
}

// TestReconcileClusterRolesForCSV covers the OlmConfig-dictated reconcile:
// policy enabled (or absent) keeps the legacy ensure behavior; policy
// disabled deletes previously generated roles, including for owned
// APIServices, and preserves out-of-band roles that collide with the
// deterministic names.
func TestReconcileClusterRolesForCSV(t *testing.T) {
	olmGeneratedRole := func(name string, ownerRef metav1.OwnerReference) *rbacv1.ClusterRole {
		block, controller := false, false
		ownerRef.Controller = &controller
		ownerRef.BlockOwnerDeletion = &block
		return &rbacv1.ClusterRole{
			TypeMeta: metav1.TypeMeta{Kind: "ClusterRole", APIVersion: rbacv1.SchemeGroupVersion.String()},
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					install.OLMManagedLabelKey:                    install.OLMManagedLabelValue,
					"rbac.authorization.k8s.io/aggregate-to-edit": "true",
				},
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Rules: []rbacv1.PolicyRule{{APIGroups: []string{"test.io"}, Resources: []string{"widgets"}, Verbs: EditVerbs}},
		}
	}
	crdOwnerRef := metav1.OwnerReference{APIVersion: "apiextensions.k8s.io/v1", Kind: "customresourcedefinition", Name: testCRDName, UID: testCRDUID}

	assertRoles := func(t *testing.T, op *Operator, names []string, wantDeleted bool) {
		t.Helper()
		for _, name := range names {
			_, err := op.opClient.KubernetesInterface().RbacV1().ClusterRoles().Get(context.TODO(), name, metav1.GetOptions{})
			if wantDeleted {
				require.True(t, apierrors.IsNotFound(err), "expected role %s to be deleted", name)
			} else {
				require.NoError(t, err, "expected role %s to exist", name)
			}
		}
	}

	t.Run("policy absent: roles ensured (legacy behavior)", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a")
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv),
			withPartialMetadata(roleAggregationTestCRDPartial()),
		)
		require.NoError(t, err)

		require.NoError(t, op.reconcileClusterRolesForCSV(csv))
		assertRoles(t, op, []string{
			testCRDName + "-v1-admin",
			testCRDName + "-v1-edit",
			testCRDName + "-v1-view",
			testCRDName + "-v1-crdview",
		}, false)
	})

	t.Run("policy disabled: previously generated roles deleted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a")
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv, roleAggregationTestOLMConfig(ptr.To(true))),
			withPartialMetadata(roleAggregationTestCRDPartial()),
			withK8sObjs(
				olmGeneratedRole(testCRDName+"-v1-admin", crdOwnerRef),
				olmGeneratedRole(testCRDName+"-v1-edit", crdOwnerRef),
				olmGeneratedRole(testCRDName+"-v1-view", crdOwnerRef),
				olmGeneratedRole(testCRDName+"-v1-crdview", crdOwnerRef),
			),
		)
		require.NoError(t, err)

		require.NoError(t, op.reconcileClusterRolesForCSV(csv))
		assertRoles(t, op, []string{
			testCRDName + "-v1-admin",
			testCRDName + "-v1-edit",
			testCRDName + "-v1-view",
			testCRDName + "-v1-crdview",
		}, true)
	})

	t.Run("policy disabled: out-of-band role with colliding name preserved", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a")
		outOfBand := olmGeneratedRole(testCRDName+"-v1-edit", crdOwnerRef)
		outOfBand.Labels = map[string]string{"someone-elses": "role"}
		outOfBand.OwnerReferences = nil
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv, roleAggregationTestOLMConfig(ptr.To(true))),
			withPartialMetadata(roleAggregationTestCRDPartial()),
			withK8sObjs(outOfBand),
		)
		require.NoError(t, err)

		require.NoError(t, op.reconcileClusterRolesForCSV(csv))
		assertRoles(t, op, []string{testCRDName + "-v1-edit"}, false)
	})

	t.Run("policy disabled: apiservice-owned roles deleted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		const apiserviceName = "v1.widgets.test.io"
		csv := roleAggregationTestCSV("a", "ns-a")
		csv.Spec.CustomResourceDefinitions = v1alpha1.CustomResourceDefinitions{}
		csv.Spec.APIServiceDefinitions = v1alpha1.APIServiceDefinitions{
			Owned: []v1alpha1.APIServiceDescription{{Name: "widgets", Group: "widgets.test.io", Version: "v1", Kind: "Widget"}},
		}
		apiservice := &apiregistrationv1.APIService{
			TypeMeta:   metav1.TypeMeta{Kind: "APIService", APIVersion: apiregistrationv1.SchemeGroupVersion.String()},
			ObjectMeta: metav1.ObjectMeta{Name: apiserviceName, UID: types.UID("apiservice-uid")},
		}
		apiserviceOwnerRef := metav1.OwnerReference{APIVersion: apiregistrationv1.SchemeGroupVersion.String(), Kind: "APIService", Name: apiserviceName, UID: types.UID("apiservice-uid")}
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv, roleAggregationTestOLMConfig(ptr.To(true))),
			withRegObjs(apiservice),
			withK8sObjs(
				olmGeneratedRole("widgets-v1-admin", apiserviceOwnerRef),
				olmGeneratedRole("widgets-v1-edit", apiserviceOwnerRef),
				olmGeneratedRole("widgets-v1-view", apiserviceOwnerRef),
			),
		)
		require.NoError(t, err)

		require.NoError(t, op.reconcileClusterRolesForCSV(csv))
		assertRoles(t, op, []string{"widgets-v1-admin", "widgets-v1-edit", "widgets-v1-view"}, true)
	})
}
