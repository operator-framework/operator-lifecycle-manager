package olm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	opregistry "github.com/operator-framework/operator-registry/pkg/registry"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
)

// testAPI describes one provided API under test.
type testAPI struct {
	crdName string
	uid     types.UID
	group   string
	plural  string
	version string
	kind    string
}

var (
	widgets = testAPI{crdName: "widgets.test.io", uid: types.UID("widgets-crd-uid"), group: "test.io", plural: "widgets", version: "v1", kind: "Widget"}
	gadgets = testAPI{crdName: "gadgets.test.io", uid: types.UID("gadgets-crd-uid"), group: "test.io", plural: "gadgets", version: "v1", kind: "Gadget"}
)

func (a testAPI) crdPartial() *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		TypeMeta:   metav1.TypeMeta{Kind: "customresourcedefinition", APIVersion: "apiextensions.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: a.crdName, UID: a.uid},
	}
}

func (a testAPI) crdDescription() v1alpha1.CRDDescription {
	return v1alpha1.CRDDescription{Name: a.crdName, Version: a.version, Kind: a.kind}
}

func (a testAPI) roleName(suffix string) string {
	return a.crdName + "-" + a.version + "-" + suffix
}

// roleNames returns the four generated role names for the API, keyed by the
// label suffix each role uses (the crdview role is created with ViewSuffix).
func (a testAPI) roleNames() map[string]string {
	return map[string]string{
		"admin":   a.roleName("admin"),
		"edit":    a.roleName("edit"),
		"view":    a.roleName("view"),
		"crdview": a.crdName + "-" + a.version + "-crdview",
	}
}

func roleAggregationTestCSV(name, namespace string, apis ...testAPI) *v1alpha1.ClusterServiceVersion {
	owned := make([]v1alpha1.CRDDescription, 0, len(apis))
	for _, a := range apis {
		owned = append(owned, a.crdDescription())
	}
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
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{Owned: owned},
		},
		Status: v1alpha1.ClusterServiceVersionStatus{Phase: v1alpha1.CSVPhaseSucceeded},
	}
}

func roleAggregationTestOLMConfig(features *operatorsv1.Features) *operatorsv1.OLMConfig {
	return &operatorsv1.OLMConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       operatorsv1.OLMConfigSpec{Features: features},
	}
}

// aggregatedRoleLabels returns the full label set OLM puts on a generated
// role of the given suffix when aggregation is enabled for the API.
func aggregatedRoleLabels(t *testing.T, a testAPI, suffix string) map[string]string {
	t.Helper()
	key := opregistry.APIKey{Group: a.group, Version: a.version, Kind: a.kind, Plural: a.plural}
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

// mkGeneratedRole builds a role exactly as OLM generates it for the API when
// aggregation is enabled.
func mkGeneratedRole(t *testing.T, a testAPI, name, labelSuffix string) *rbacv1.ClusterRole {
	t.Helper()
	block, controller := false, false
	return &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{Kind: "ClusterRole", APIVersion: rbacv1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: aggregatedRoleLabels(t, a, labelSuffix),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         "apiextensions.k8s.io/v1",
				Kind:               "customresourcedefinition",
				Name:               a.crdName,
				UID:                a.uid,
				Controller:         &controller,
				BlockOwnerDeletion: &block,
			}},
		},
		Rules: []rbacv1.PolicyRule{{Verbs: EditVerbs, APIGroups: []string{a.group}, Resources: []string{a.plural}}},
	}
}

// TestRoleAggregationPolicyIsEnabledFor covers the internal policy
// evaluation with the default-deny-with-allowlist semantics: unset disable
// keeps the legacy behavior for every API (defensively ignoring an
// allowlist that could only have been set through an unvalidated path);
// roleAggregation.disable=true disables aggregation for every API except
// allowlist matches, and an empty allowlist disables it for every API.
// Matching is exact per group and kind, independent of version.
func TestRoleAggregationPolicyIsEnabledFor(t *testing.T) {
	widgetsGK := operatorsv1.GroupKind{Group: widgets.group, Kind: widgets.kind}
	gadgetsGK := operatorsv1.GroupKind{Group: gadgets.group, Kind: gadgets.kind}

	table := []struct {
		name     string
		olmcfg   *operatorsv1.OLMConfig
		query    operatorsv1.GroupKind
		expected bool
	}{
		{name: "nil config", query: widgetsGK, expected: true},
		{name: "nil features", olmcfg: roleAggregationTestOLMConfig(nil), query: widgetsGK, expected: true},
		{name: "unset disable keeps legacy behavior for every api", olmcfg: roleAggregationTestOLMConfig(&operatorsv1.Features{}), query: widgetsGK, expected: true},
		{name: "unset disable defensively ignores an allowlist", olmcfg: roleAggregationTestOLMConfig(&operatorsv1.Features{RoleAggregation: operatorsv1.OLMConfigRoleAggregation{EnableAPIs: []operatorsv1.GroupKind{widgetsGK}}}), query: gadgetsGK, expected: true},
		{name: "disabled with empty allowlist aggregates nothing", olmcfg: roleAggregationTestOLMConfig(&operatorsv1.Features{RoleAggregation: operatorsv1.OLMConfigRoleAggregation{Disable: true}}), query: widgetsGK, expected: false},
		{name: "disabled with allowlist entry matching the api", olmcfg: roleAggregationTestOLMConfig(&operatorsv1.Features{RoleAggregation: operatorsv1.OLMConfigRoleAggregation{Disable: true, EnableAPIs: []operatorsv1.GroupKind{widgetsGK}}}), query: widgetsGK, expected: true},
		{name: "disabled with allowlist entry for another kind", olmcfg: roleAggregationTestOLMConfig(&operatorsv1.Features{RoleAggregation: operatorsv1.OLMConfigRoleAggregation{Disable: true, EnableAPIs: []operatorsv1.GroupKind{widgetsGK}}}), query: gadgetsGK, expected: false},
	}
	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			policy := newRoleAggregationPolicy(tt.olmcfg)
			require.Equal(t, tt.expected, policy.isEnabledFor(tt.query.Group, tt.query.Kind))
		})
	}
}

// TestEnsureClusterRolesForCSVAggregationLabels covers the OlmConfig-dictated
// coupling of the aggregation labels: the default policy keeps the legacy
// behavior (roles generated with both aggregation labels); restrictive mode
// (roleAggregation.disable=true) maintains roles without the labels except
// for allowlisted APIs, with the existing label-equality update path
// converging roles generated under a previous policy in either direction
// while preserving their rules.
func TestEnsureClusterRolesForCSVAggregationLabels(t *testing.T) {
	// roleSuffixes maps each generated role to the suffix used for its
	// aggregation labels (the crdview role is created with ViewSuffix).
	// The crdview role always aggregates (CRD get for discovery), so it is
	// excluded from policy-driven label assertions.
	roleSuffixes := map[string]string{"admin": "admin", "edit": "edit", "view": "view"}

	assertLabels := func(t *testing.T, op *Operator, a testAPI, aggregated bool) {
		t.Helper()
		for kind, name := range a.roleNames() {
			role := getRole(t, op, name)
			require.Equal(t, install.OLMManagedLabelValue, role.Labels[install.OLMManagedLabelKey], "%s %s must remain OLM-managed", a.crdName, kind)
			if kind == "crdview" {
				// crdview always aggregates: viewers need CRD get for discovery
				for label := range aggregatedRoleLabels(t, a, "view") {
					if label == install.OLMManagedLabelKey {
						continue
					}
					require.Equal(t, "true", role.Labels[label], "%s crdview must always carry aggregation label %s", a.crdName, label)
				}
				continue
			}
			for label := range aggregatedRoleLabels(t, a, roleSuffixes[kind]) {
				if label == install.OLMManagedLabelKey {
					continue
				}
				if aggregated {
					require.Equal(t, "true", role.Labels[label], "%s %s must carry aggregation label %s", a.crdName, kind, label)
				} else {
					require.NotContains(t, role.Labels, label, "%s %s must not carry aggregation label %s", a.crdName, kind, label)
				}
			}
		}
	}

	t.Run("policy absent: roles generated with aggregation labels (legacy behavior)", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a", widgets)
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv),
			withPartialMetadata(widgets.crdPartial()),
		)
		require.NoError(t, err)

		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, widgets, true)
	})

	t.Run("restrictive mode with empty allowlist: roles generated without aggregation labels", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a", widgets)
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv, roleAggregationTestOLMConfig(&operatorsv1.Features{RoleAggregation: operatorsv1.OLMConfigRoleAggregation{Disable: true}})),
			withPartialMetadata(widgets.crdPartial()),
		)
		require.NoError(t, err)

		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, widgets, false)
		// rules are still maintained
		edit := getRole(t, op, widgets.roleName("edit"))
		require.Equal(t, []rbacv1.PolicyRule{{Verbs: EditVerbs, APIGroups: []string{widgets.group}, Resources: []string{widgets.plural}}}, edit.Rules)
	})

	t.Run("restrictive mode transition: aggregation labels removed, rules preserved", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a", widgets)
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv, roleAggregationTestOLMConfig(&operatorsv1.Features{RoleAggregation: operatorsv1.OLMConfigRoleAggregation{Disable: true}})),
			withPartialMetadata(widgets.crdPartial()),
			withK8sObjs(
				mkGeneratedRole(t, widgets, widgets.roleName("admin"), "admin"),
				mkGeneratedRole(t, widgets, widgets.roleName("edit"), "edit"),
				mkGeneratedRole(t, widgets, widgets.roleName("view"), "view"),
				mkGeneratedRole(t, widgets, widgets.crdName+"-"+widgets.version+"-crdview", "view"),
			),
		)
		require.NoError(t, err)

		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, widgets, false)
		edit := getRole(t, op, widgets.roleName("edit"))
		require.Equal(t, []rbacv1.PolicyRule{{Verbs: EditVerbs, APIGroups: []string{widgets.group}, Resources: []string{widgets.plural}}}, edit.Rules)
	})

	t.Run("re-enable transition: aggregation labels re-added", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a", widgets)
		deLabeled := mkGeneratedRole(t, widgets, widgets.roleName("edit"), "edit")
		deLabeled.Labels = map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv), // no OLMConfig: aggregation enabled
			withPartialMetadata(widgets.crdPartial()),
			withK8sObjs(deLabeled),
		)
		require.NoError(t, err)

		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, widgets, true)
	})

	t.Run("restrictive mode with allowlist: only allowlisted apis keep aggregation, others de-labeled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		csv := roleAggregationTestCSV("a", "ns-a", widgets, gadgets)
		widgetsGK := operatorsv1.GroupKind{Group: widgets.group, Kind: widgets.kind}
		olmcfg := roleAggregationTestOLMConfig(&operatorsv1.Features{
			RoleAggregation: operatorsv1.OLMConfigRoleAggregation{
				Disable:    true,
				EnableAPIs: []operatorsv1.GroupKind{widgetsGK},
			},
		})
		// Pre-existing roles for BOTH apis as generated under the legacy
		// policy: gadgets must be de-labeled, widgets must keep its labels.
		mkAll := func(a testAPI) []runtime.Object {
			return []runtime.Object{
				mkGeneratedRole(t, a, a.roleName("admin"), "admin"),
				mkGeneratedRole(t, a, a.roleName("edit"), "edit"),
				mkGeneratedRole(t, a, a.roleName("view"), "view"),
				mkGeneratedRole(t, a, a.crdName+"-"+a.version+"-crdview", "view"),
			}
		}
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv, olmcfg),
			withPartialMetadata(widgets.crdPartial(), gadgets.crdPartial()),
			withK8sObjs(append(mkAll(widgets), mkAll(gadgets)...)...),
		)
		require.NoError(t, err)

		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, widgets, true)
		assertLabels(t, op, gadgets, false)
		// rules are still maintained for the de-labeled api
		edit := getRole(t, op, gadgets.roleName("edit"))
		require.Equal(t, []rbacv1.PolicyRule{{Verbs: EditVerbs, APIGroups: []string{gadgets.group}, Resources: []string{gadgets.plural}}}, edit.Rules)
	})

	t.Run("version independence: version-free entry matches any owned version of the kind", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()
		widgetsV2 := widgets
		widgetsV2.version = "v2"
		csv := roleAggregationTestCSV("a", "ns-a", widgetsV2)
		widgetsGK := operatorsv1.GroupKind{Group: widgets.group, Kind: widgets.kind}
		olmcfg := roleAggregationTestOLMConfig(&operatorsv1.Features{
			RoleAggregation: operatorsv1.OLMConfigRoleAggregation{
				Disable:    true,
				EnableAPIs: []operatorsv1.GroupKind{widgetsGK},
			},
		})
		op, err := NewFakeOperator(ctx,
			withClientObjs(csv, olmcfg),
			withPartialMetadata(widgetsV2.crdPartial()),
		)
		require.NoError(t, err)

		// The entry carries no version, yet the v2-owned api is aggregated:
		// matching is keyed on group+kind alone.
		require.NoError(t, op.ensureClusterRolesForCSV(csv))
		assertLabels(t, op, widgetsV2, true)
	})
}
