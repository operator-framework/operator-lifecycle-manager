package olm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	opregistry "github.com/operator-framework/operator-registry/pkg/registry"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
)

const (
	testCRDName = "widgets.test.io"
	testCRDUID  = types.UID("crd-uid")
	testGroup   = "test.io"
	testPlural  = "widgets"
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

// aggregatedRoleLabels returns the full label set OLM puts on a generated
// role when aggregation is enabled, for the given suffix.
func aggregatedRoleLabels(t *testing.T, suffix string) map[string]string {
	t.Helper()
	key := opregistry.APIKey{Group: testGroup, Version: "v1", Kind: "Widget", Plural: testPlural}
	hash, err := cache.APIKeyToGVKHash(key)
	require.NoError(t, err)
	return map[string]string{
		install.OLMManagedLabelKey:                                    install.OLMManagedLabelValue,
		"rbac.authorization.k8s.io/aggregate-to-" + suffix:            "true",
		"olm.opgroup.permissions/aggregate-to-" + hash + "-" + suffix: "true",
	}
}

// getRole fetches a ClusterRole from the (fake) cluster.
func getRole(t *testing.T, op *Operator, name string) *rbacv1.ClusterRole {
	t.Helper()
	role, err := op.opClient.KubernetesInterface().RbacV1().ClusterRoles().Get(context.TODO(), name, metav1.GetOptions{})
	require.NoError(t, err)
	return role
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

// TestEnsureClusterRolesForCSVAggregationLabels covers the OlmConfig-dictated
// coupling of the aggregation labels: enabled (or absent) policy keeps the
// legacy behavior (roles generated with both aggregation labels); disabled
// policy maintains the roles without them. Both policy transitions converge
// through the existing label-equality update path: a role generated under
// the previous policy has its aggregation labels removed (disable) or
// re-added (enable) while its rules are preserved.
func TestEnsureClusterRolesForCSVAggregationLabels(t *testing.T) {
	roleNames := map[string]string{
		"admin":   testCRDName + "-v1-admin",
		"edit":    testCRDName + "-v1-edit",
		"view":    testCRDName + "-v1-view",
		"crdview": testCRDName + "-v1-crdview",
	}
	// The suffix each role name uses for its aggregation labels.
	roleSuffixes := map[string]string{
		"admin":   "admin",
		"edit":    "edit",
		"view":    "view",
		"crdview": "view", // the crdview role is created with ViewSuffix
	}

	assertLabels := func(t *testing.T, op *Operator, aggregated bool) {
		t.Helper()
		for kind, name := range roleNames {
			role := getRole(t, op, name)
			require.Equal(t, install.OLMManagedLabelValue, role.Labels[install.OLMManagedLabelKey], "%s must remain OLM-managed", kind)
			for label := range aggregatedRoleLabels(t, roleSuffixes[kind]) {
				if label == install.OLMManagedLabelKey {
					continue
				}
				if aggregated {
					require.Equal(t, "true", role.Labels[label], "%s must carry aggregation label %s", kind, label)
				} else {
					require.NotContains(t, role.Labels, label, "%s must not carry aggregation label %s", kind, label)
				}
			}
		}
	}

	t.Run("policy absent: roles generated with aggregation labels (legacy behavior)", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a")
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv),
			withPartialMetadata(roleAggregationTestCRDPartial()),
		)
		require.NoError(t, err)

		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, true)
	})

	t.Run("policy disabled: roles generated without aggregation labels", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a")
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv, roleAggregationTestOLMConfig(ptr.To(true))),
			withPartialMetadata(roleAggregationTestCRDPartial()),
		)
		require.NoError(t, err)

		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, false)
		// rules are still maintained
		edit := getRole(t, op, roleNames["edit"])
		require.Equal(t, []rbacv1.PolicyRule{{Verbs: EditVerbs, APIGroups: []string{testGroup}, Resources: []string{testPlural}}}, edit.Rules)
	})

	t.Run("policy transition to disabled: aggregation labels removed, rules preserved", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a")
		// Pre-existing roles exactly as generated under the enabled policy.
		block, controller := false, false
		mkRole := func(suffix, name string) *rbacv1.ClusterRole {
			return &rbacv1.ClusterRole{
				TypeMeta: metav1.TypeMeta{Kind: "ClusterRole", APIVersion: rbacv1.SchemeGroupVersion.String()},
				ObjectMeta: metav1.ObjectMeta{
					Name:   name,
					Labels: aggregatedRoleLabels(t, suffix),
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion:         "apiextensions.k8s.io/v1",
						Kind:               "customresourcedefinition",
						Name:               testCRDName,
						UID:                testCRDUID,
						Controller:         &controller,
						BlockOwnerDeletion: &block,
					}},
				},
				Rules: []rbacv1.PolicyRule{{Verbs: EditVerbs, APIGroups: []string{testGroup}, Resources: []string{testPlural}}},
			}
		}
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv, roleAggregationTestOLMConfig(ptr.To(true))),
			withPartialMetadata(roleAggregationTestCRDPartial()),
			withK8sObjs(
				mkRole("admin", roleNames["admin"]),
				mkRole("edit", roleNames["edit"]),
				mkRole("view", roleNames["view"]),
				mkRole("view", roleNames["crdview"]),
			),
		)
		require.NoError(t, err)

		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, false)
		edit := getRole(t, op, roleNames["edit"])
		require.Equal(t, []rbacv1.PolicyRule{{Verbs: EditVerbs, APIGroups: []string{testGroup}, Resources: []string{testPlural}}}, edit.Rules)
	})

	t.Run("policy transition to enabled: aggregation labels re-added", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a")
		// Pre-existing role as generated under the disabled policy: managed,
		// with rules, but without aggregation labels.
		block, controller := false, false
		deLabeled := &rbacv1.ClusterRole{
			TypeMeta: metav1.TypeMeta{Kind: "ClusterRole", APIVersion: rbacv1.SchemeGroupVersion.String()},
			ObjectMeta: metav1.ObjectMeta{
				Name:   roleNames["edit"],
				Labels: map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion:         "apiextensions.k8s.io/v1",
					Kind:               "customresourcedefinition",
					Name:               testCRDName,
					UID:                testCRDUID,
					Controller:         &controller,
					BlockOwnerDeletion: &block,
				}},
			},
			Rules: []rbacv1.PolicyRule{{Verbs: EditVerbs, APIGroups: []string{testGroup}, Resources: []string{testPlural}}},
		}
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv), // no OLMConfig: aggregation enabled
			withPartialMetadata(roleAggregationTestCRDPartial()),
			withK8sObjs(deLabeled),
		)
		require.NoError(t, err)

		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, true)
	})
}
