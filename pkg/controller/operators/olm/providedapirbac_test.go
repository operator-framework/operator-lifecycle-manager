package olm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
)

const (
	testCRDName  = "widgets.test.io"
	testCRDUID   = types.UID("crd-uid")
	testCRDGVK   = "customresourcedefinition"
	testCRDGroup = "apiextensions.k8s.io"
)

func rbacTestCSV(name, namespace string, phase v1alpha1.ClusterServiceVersionPhase, annotations map[string]string, crdNames ...string) *v1alpha1.ClusterServiceVersion {
	owned := make([]v1alpha1.CRDDescription, 0, len(crdNames))
	for _, n := range crdNames {
		owned = append(owned, v1alpha1.CRDDescription{Name: n, Version: "v1", Kind: "Widget"})
	}
	return &v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			UID:         types.UID(name + "-" + namespace),
			Annotations: annotations,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{Owned: owned},
		},
		Status: v1alpha1.ClusterServiceVersionStatus{Phase: phase},
	}
}

func rbacTestCRDPartial() *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		TypeMeta:   metav1.TypeMeta{Kind: testCRDGVK, APIVersion: testCRDGroup + "/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: testCRDName, UID: testCRDUID},
	}
}

// TestCSVDisablesRoleAggregation covers the signal parsing: both annotation
// shapes (resolver projection and raw olm.properties), absence, malformed
// input, and unrelated property types.
func TestCSVDisablesRoleAggregation(t *testing.T) {
	table := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name: "no annotations",
		},
		{
			name:        "projection annotation with opt-out",
			annotations: map[string]string{projection.PropertiesAnnotationKey: `{"properties":[{"type":"olm.rbac.role-aggregation","value":{"disabled":true}}]}`},
			expected:    true,
		},
		{
			name:        "projection annotation with disabled=false",
			annotations: map[string]string{projection.PropertiesAnnotationKey: `{"properties":[{"type":"olm.rbac.role-aggregation","value":{"disabled":false}}]}`},
		},
		{
			name:        "projection annotation with unrelated property",
			annotations: map[string]string{projection.PropertiesAnnotationKey: `{"properties":[{"type":"olm.package","value":{"packageName":"test","version":"1.0.0"}}]}`},
		},
		{
			name:        "raw olm.properties annotation with opt-out",
			annotations: map[string]string{olmPropertiesAnnotationKey: `[{"type":"olm.rbac.role-aggregation","value":{"disabled":true}}]`},
			expected:    true,
		},
		{
			name:        "raw olm.properties annotation with unrelated property",
			annotations: map[string]string{olmPropertiesAnnotationKey: `[{"type":"olm.package","value":{"packageName":"test","version":"1.0.0"}}]`},
		},
		{
			name:        "malformed annotations are ignored",
			annotations: map[string]string{projection.PropertiesAnnotationKey: `{not json`, olmPropertiesAnnotationKey: `[also not`},
		},
	}
	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			csv := rbacTestCSV("csv", "ns", v1alpha1.CSVPhaseSucceeded, tt.annotations, testCRDName)
			require.Equal(t, tt.expected, csvDisablesRoleAggregation(csv))
		})
	}
}

// TestCSVIsActiveProviderOf covers the provider filter used by the fold.
func TestCSVIsActiveProviderOf(t *testing.T) {
	copied := rbacTestCSV("copied", "ns", v1alpha1.CSVPhaseSucceeded, nil, testCRDName)
	copied.Status.Reason = v1alpha1.CSVReasonCopied

	deleting := rbacTestCSV("deleting", "ns", v1alpha1.CSVPhaseSucceeded, nil, testCRDName)
	now := metav1.Now()
	deleting.DeletionTimestamp = &now

	otherCRD := rbacTestCSV("other-crd", "ns", v1alpha1.CSVPhaseSucceeded, nil, "other.test.io")

	table := []struct {
		name     string
		csv      *v1alpha1.ClusterServiceVersion
		expected bool
	}{
		{name: "nil", csv: nil},
		{name: "succeeded provider", csv: rbacTestCSV("ok", "ns", v1alpha1.CSVPhaseSucceeded, nil, testCRDName), expected: true},
		{name: "pending provider", csv: rbacTestCSV("pending", "ns", v1alpha1.CSVPhasePending, nil, testCRDName)},
		{name: "failed provider", csv: rbacTestCSV("failed", "ns", v1alpha1.CSVPhaseFailed, nil, testCRDName)},
		{name: "copied csv", csv: copied},
		{name: "deleting csv", csv: deleting},
		{name: "provider of another crd", csv: otherCRD},
	}
	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, csvIsActiveProviderOf(tt.csv, testCRDName))
		})
	}
}

// TestRoleAggregationVetoed covers the per-API fold: a signal on any active
// provider of the CRD vetoes, regardless of namespace.
func TestRoleAggregationVetoed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	projectionOptOut := map[string]string{
		projection.PropertiesAnnotationKey: `{"properties":[{"type":"olm.rbac.role-aggregation","value":{"disabled":true}}]}`,
	}
	rawOptOut := map[string]string{
		olmPropertiesAnnotationKey: `[{"type":"olm.rbac.role-aggregation","value":{"disabled":true}}]`,
	}

	table := []struct {
		name     string
		csvs     []*v1alpha1.ClusterServiceVersion
		expected bool
	}{
		{
			name: "single unflagged provider",
			csvs: []*v1alpha1.ClusterServiceVersion{rbacTestCSV("a", "ns-a", v1alpha1.CSVPhaseSucceeded, nil, testCRDName)},
		},
		{
			name:     "flagged provider vetoes",
			csvs:     []*v1alpha1.ClusterServiceVersion{rbacTestCSV("a", "ns-a", v1alpha1.CSVPhaseSucceeded, nil, testCRDName), rbacTestCSV("b", "ns-b", v1alpha1.CSVPhaseSucceeded, projectionOptOut, testCRDName)},
			expected: true,
		},
		{
			name:     "flagged provider in other namespace carries raw annotation",
			csvs:     []*v1alpha1.ClusterServiceVersion{rbacTestCSV("a", "ns-a", v1alpha1.CSVPhaseSucceeded, nil, testCRDName), rbacTestCSV("b", "ns-b", v1alpha1.CSVPhaseSucceeded, rawOptOut, testCRDName)},
			expected: true,
		},
		{
			name: "flagged but not active is ignored",
			csvs: []*v1alpha1.ClusterServiceVersion{
				rbacTestCSV("a", "ns-a", v1alpha1.CSVPhaseSucceeded, nil, testCRDName),
				rbacTestCSV("b-pending", "ns-b", v1alpha1.CSVPhasePending, projectionOptOut, testCRDName),
				rbacTestCSV("b-failed", "ns-c", v1alpha1.CSVPhaseFailed, projectionOptOut, testCRDName),
			},
		},
		{
			name: "flagged provider of another crd is ignored",
			csvs: []*v1alpha1.ClusterServiceVersion{
				rbacTestCSV("a", "ns-a", v1alpha1.CSVPhaseSucceeded, nil, testCRDName),
				rbacTestCSV("b", "ns-b", v1alpha1.CSVPhaseSucceeded, projectionOptOut, "other.test.io"),
			},
		},
	}
	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			op, err := NewFakeOperator(ctx, withClientObjs(toRuntimeObjects(tt.csvs)...))
			require.NoError(t, err)
			vetoed, err := op.roleAggregationVetoed(testCRDName)
			require.NoError(t, err)
			require.Equal(t, tt.expected, vetoed)
		})
	}
}

func toRuntimeObjects(csvs []*v1alpha1.ClusterServiceVersion) []runtime.Object {
	objs := make([]runtime.Object, 0, len(csvs))
	for _, csv := range csvs {
		objs = append(objs, csv)
	}
	return objs
}

// TestReconcileProvidedAPIRBAC covers the reconcile itself: the fold decides
// for every provider of an API, so syncing an UNFLAGGED CSV must delete the
// shared roles when another active provider carries the opt-out, must
// (re)create them when no provider does, and must never delete out-of-band
// roles that merely collide with the deterministic names.
func TestReconcileProvidedAPIRBAC(t *testing.T) {
	projectionOptOut := map[string]string{
		projection.PropertiesAnnotationKey: `{"properties":[{"type":"olm.rbac.role-aggregation","value":{"disabled":true}}]}`,
	}
	roleName := testCRDName + "-v1-edit"

	olmGeneratedRole := func(name string) *rbacv1.ClusterRole {
		block, controller := false, false
		return &rbacv1.ClusterRole{
			TypeMeta: metav1.TypeMeta{Kind: "ClusterRole", APIVersion: rbacv1.SchemeGroupVersion.String()},
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					install.OLMManagedLabelKey:                    install.OLMManagedLabelValue,
					"rbac.authorization.k8s.io/aggregate-to-edit": "true",
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion:         testCRDGroup + "/v1",
					Kind:               testCRDGVK,
					Name:               testCRDName,
					UID:                testCRDUID,
					Controller:         &controller,
					BlockOwnerDeletion: &block,
				}},
			},
			Rules: []rbacv1.PolicyRule{{APIGroups: []string{"test.io"}, Resources: []string{"widgets"}, Verbs: EditVerbs}},
		}
	}

	t.Run("no provider opts out: roles ensured", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := rbacTestCSV("a", "ns-a", v1alpha1.CSVPhaseSucceeded, nil, testCRDName)
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv),
			withPartialMetadata(rbacTestCRDPartial()),
		)
		require.NoError(t, err)

		require.NoError(t, op.reconcileProvidedAPIRBAC(csv))
		for _, suffix := range []string{"admin", "edit", "view", "crdview"} {
			_, err := op.opClient.KubernetesInterface().RbacV1().ClusterRoles().Get(context.TODO(), testCRDName+"-v1-"+suffix, metav1.GetOptions{})
			require.NoError(t, err, "expected role %s to be created", suffix)
		}
	})

	t.Run("another provider opts out: roles deleted even when syncing the unflagged csv", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		unflagged := rbacTestCSV("a", "ns-a", v1alpha1.CSVPhaseSucceeded, nil, testCRDName)
		flagged := rbacTestCSV("b", "ns-b", v1alpha1.CSVPhaseSucceeded, projectionOptOut, testCRDName)
		op, err := NewFakeOperator(ctx,
			withClientObjs(unflagged, flagged),
			withPartialMetadata(rbacTestCRDPartial()),
			withK8sObjs(
				olmGeneratedRole(testCRDName+"-v1-admin"),
				olmGeneratedRole(roleName),
				olmGeneratedRole(testCRDName+"-v1-view"),
				olmGeneratedRole(testCRDName+"-v1-crdview"),
			),
		)
		require.NoError(t, err)

		// Sync the UNFLAGGED provider: the fold must still see the other
		// provider's signal and delete the shared roles.
		require.NoError(t, op.reconcileProvidedAPIRBAC(unflagged))
		for _, suffix := range []string{"admin", "edit", "view", "crdview"} {
			_, err := op.opClient.KubernetesInterface().RbacV1().ClusterRoles().Get(context.TODO(), testCRDName+"-v1-"+suffix, metav1.GetOptions{})
			require.True(t, apierrors.IsNotFound(err), "expected role %s to be deleted", suffix)
		}
	})

	t.Run("out-of-band role with colliding name is preserved", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		unflagged := rbacTestCSV("a", "ns-a", v1alpha1.CSVPhaseSucceeded, nil, testCRDName)
		flagged := rbacTestCSV("b", "ns-b", v1alpha1.CSVPhaseSucceeded, projectionOptOut, testCRDName)
		outOfBand := olmGeneratedRole(roleName)
		outOfBand.Labels = map[string]string{"someone-elses": "role"} // no olm.managed, no ownerRef below
		outOfBand.OwnerReferences = nil
		op, err := NewFakeOperator(ctx,
			withClientObjs(unflagged, flagged),
			withPartialMetadata(rbacTestCRDPartial()),
			withK8sObjs(outOfBand),
		)
		require.NoError(t, err)

		require.NoError(t, op.reconcileProvidedAPIRBAC(unflagged))
		_, err = op.opClient.KubernetesInterface().RbacV1().ClusterRoles().Get(context.TODO(), roleName, metav1.GetOptions{})
		require.NoError(t, err, "out-of-band role must not be deleted")
	})
}
