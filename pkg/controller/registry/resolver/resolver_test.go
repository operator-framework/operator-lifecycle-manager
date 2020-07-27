package resolver

import (
	"testing"

	"github.com/blang/semver"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

func TestSolveOperators(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1", "1.0.1", "", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)

	expected := OperatorSet{
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageB.v1": genOperator("packageB.v1", "1.0.1", "", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
	}
	require.EqualValues(t, expected, operators)
}

func TestSolveOperators_MultipleChannels(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "beta", "community", "olm", nil, nil, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	expected := OperatorSet{
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
	}
	assert.Equal(t, 2, len(operators))
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}
}

func TestSolveOperators_FindLatestVersion(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v0.9.0", "0.9.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB.v0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(operators))
	for _, op := range operators {
		assert.Equal(t, "1.0.1", op.Version().String())
	}

	expected := OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
	}
	assert.Equal(t, 2, len(operators))
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}
}

func TestSolveOperators_FindLatestVersionWithDependencies(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	opToAddVersionDeps := []*api.Dependency{
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageC","version":"1.0.1"}`,
		},
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageD","version":"1.0.1"}`,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v0.9.0", "0.9.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB.v0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, ""),
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageD.v1.0.0", "1.0.0", "", "packageD", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD.v1.0.0", "packageD", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageD.v1.0.2", "1.0.2", "packageD.v1.0.1", "packageD", "alpha", "community", "olm", nil, nil, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
		log:   logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))

	expected := OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, ""),
		"packageC.v1.0.1": genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageD.v1.0.1": genOperator("packageD.v1.0.1", "1.0.1", "packageD.v1.0.0", "packageD", "alpha", "community", "olm", nil, nil, nil, ""),
	}
	for _, o := range operators {
		t.Logf("%#v", o)
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Identifier())
	}
}

func TestSolveOperators_FindLatestVersionWithNestedDependencies(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	opToAddVersionDeps := []*api.Dependency{
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageC","version":"1.0.1"}`,
		},
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageD","version":"1.0.1"}`,
		},
	}
	nestedVersionDeps := []*api.Dependency{
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageE","version":"1.0.1"}`,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v0.9.0", "0.9.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB.v0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, ""),
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "community", "olm", nil, nil, nestedVersionDeps, ""),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nestedVersionDeps, ""),
					genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageE.v1.0.1", "1.0.1", "packageE.v1.0.0", "packageE", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageE.v1.0.0", "1.0.0", "", "packageE", "alpha", "community", "olm", nil, nil, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 5, len(operators))

	expected := OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, ""),
		"packageC.v1.0.1": genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageD.v1.0.1": genOperator("packageD.v1.0.1", "1.0.1", "packageD.v1.0.0", "packageD", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageE.v1.0.1": genOperator("packageE.v1.0.1", "1.0.1", "packageE.v1.0.0", "packageE", "alpha", "community", "olm", nil, nil, nil, ""),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Identifier())
	}
}

func TestSolveOperators_CatsrcPrioritySorting(t *testing.T) {
	opToAddVersionDeps := []*api.Dependency{
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageB","version":"0.0.1"}`,
		},
	}

	namespace := "olm"
	customCatalog := registry.CatalogKey{"community", namespace}
	newSub := newSub(namespace, "packageA", "alpha", customCatalog)
	subs := []*v1alpha1.Subscription{newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", namespace, nil,
						nil, opToAddVersionDeps, ""),
				},
			},
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community-operator",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community-operator",
				},
				operators: []*Operator{
					genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community-operator",
						namespace, nil, nil, nil, ""),
				},
			},
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "high-priority-operator",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "high-priority-operator",
				},
				priority: catalogSourcePriority(100),
				operators: []*Operator{
					genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "high-priority-operator",
						namespace, nil, nil, nil, ""),
				},
			},
		},
	}

	// operators sorted by priority.
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, []*v1alpha1.ClusterServiceVersion{}, subs)
	assert.NoError(t, err)
	expected := OperatorSet{
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm",
			nil, nil, opToAddVersionDeps, ""),
		"packageB.v1": genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "high-priority-operator", "olm",
			nil, nil, nil, ""),
	}
	assert.Equal(t, 2, len(operators))
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}

	// Catsrc with the same priority, ns, different name
	fakeNamespacedOperatorCache.snapshots[registry.CatalogKey{
		Namespace: "olm",
		Name:      "community-operator",
	}] = &CatalogSnapshot{
		key: registry.CatalogKey{
			Namespace: "olm",
			Name:      "community-operator",
		},
		priority: catalogSourcePriority(100),
		operators: []*Operator{
			genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community-operator",
				namespace, nil, nil, nil, ""),
		},
	}

	satResolver = SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err = satResolver.SolveOperators([]string{"olm"}, []*v1alpha1.ClusterServiceVersion{}, subs)
	assert.NoError(t, err)
	expected = OperatorSet{
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm",
			nil, nil, opToAddVersionDeps, ""),
		"packageB.v1": genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community-operator", "olm",
			nil, nil, nil, ""),
	}
	assert.Equal(t, 2, len(operators))
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}

	// operators from the same catalogs source should be prioritized.
	fakeNamespacedOperatorCache.snapshots[registry.CatalogKey{
		Namespace: "olm",
		Name:      "community",
	}] = &CatalogSnapshot{
		key: registry.CatalogKey{
			Namespace: "olm",
			Name:      "community",
		},
		operators: []*Operator{
			genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", namespace, nil,
				nil, opToAddVersionDeps, ""),
			genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community",
				namespace, nil, nil, nil, ""),
		},
	}

	satResolver = SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err = satResolver.SolveOperators([]string{"olm"}, []*v1alpha1.ClusterServiceVersion{}, subs)
	assert.NoError(t, err)
	expected = OperatorSet{
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm",
			nil, nil, opToAddVersionDeps, ""),
		"packageB.v1": genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community", "olm",
			nil, nil, nil, ""),
	}
	assert.Equal(t, 2, len(operators))
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}

}

func TestSolveOperators_WithDependencies(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	opToAddVersionDeps := []*api.Dependency{
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageC","version":"0.1.0"}`,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, ""),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, nil, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(operators))

	expected := OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageB.v1":     genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, ""),
		"packageC.v1":     genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, nil, nil, ""),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Identifier())
	}
}

func TestSolveOperators_WithGVKDependencies(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	deps := []*api.Dependency{
		{
			Type:  "olm.gvk",
			Value: `{"group":"g","kind":"k","version":"v"}`,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", Provides, nil, deps, ""),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, Provides, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(operators))

	expected := OperatorSet{
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", Provides, nil, deps, ""),
		"packageC.v1": genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, Provides, nil, ""),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Identifier())
	}
}

func TestSolveOperators_WithNestedGVKDependencies(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	deps := []*api.Dependency{
		{
			Type:  "olm.gvk",
			Value: `{"group":"g","kind":"k","version":"v"}`,
		},
	}

	deps2 := []*api.Dependency{
		{
			Type:  "olm.gvk",
			Value: `{"group":"g2","kind":"k2","version":"v2"}`,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1.0.0", "1.0.0", "", "packageB", "alpha", "community", "olm", Provides, nil, deps, ""),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, deps, ""),
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "community", "olm", Provides2, Provides, deps2, ""),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", Provides2, Provides, deps2, ""),
					genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "community", "olm", nil, Provides2, deps2, ""),
				},
			},
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "certified",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "certified",
				},
				operators: []*Operator{
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "certified", "olm", Provides2, Provides, deps2, ""),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "certified", "olm", Provides2, Provides, deps2, ""),
					genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "certified", "olm", nil, Provides2, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
		log:   logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))
	expected := OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, deps, ""),
		"packageC.v1.0.1": genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", Provides2, Provides, deps2, ""),
		"packageD.v1.0.1": genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "community", "olm", nil, Provides2, deps2, ""),
	}
	got := []string{}
	for _, o := range operators {
		got = append(got, o.Identifier())
	}
	for k := range expected {
		assert.NotNil(t, operators[k], "did not find expected operator %s in results. have: %s", k, got)
		if _, ok := operators[k]; ok {
			assert.EqualValues(t, k, operators[k].Identifier())
		}
	}
}

func TestSolveOperators_IgnoreUnsatisfiableDependencies(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	opToAddVersionDeps := []*api.Dependency{
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageC","version":"0.1.0"}`,
		},
	}
	unsatisfiableVersionDeps := []*api.Dependency{
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageD","version":"0.1.0"}`,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, ""),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, nil, unsatisfiableVersionDeps, ""),
				},
			},
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "certified",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "certified",
				},
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "certified", "olm", nil, nil, nil, ""),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "certified", "olm", nil, nil, opToAddVersionDeps, ""),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "certified", "olm", nil, nil, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(operators))

	expected := OperatorSet{
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "certified", "olm", nil, nil, opToAddVersionDeps, ""),
		"packageC.v1": genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "certified", "olm", nil, nil, nil, ""),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Identifier())
	}
}

// Behavior: The resolver should prefer catalogs in the same namespace as the subscription.
// It should also prefer the same catalog over global catalogs in terms of the operator cache.
func TestSolveOperators_PreferCatalogInSameNamespace(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	altNamespace := "alt-olm"
	catalog := registry.CatalogKey{"community", namespace}
	altnsCatalog := registry.CatalogKey{"alt-community", altNamespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			catalog: {
				operators: []*Operator{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil, ""),
				},
			},
			altnsCatalog: {
				operators: []*Operator{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", altnsCatalog.Name, altnsCatalog.Namespace, nil, Provides, nil, ""),
				},
			},
		},
		namespaces: []string{namespace, altNamespace},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, csvs, subs)
	assert.NoError(t, err)

	expected := OperatorSet{
		"packageA.v0.0.1": genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil, ""),
	}
	require.EqualValues(t, expected, operators)
}

// Behavior: The resolver should not look in catalogs not in the same namespace or the global catalog namespace when resolving the subscription.
// This test should not result in a successful resolution because the catalog fulfilling the subscription is not in the operator cache.
func TestSolveOperators_ResolveOnlyInCachedNamespaces(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}
	otherCatalog := registry.CatalogKey{Name: "secret", Namespace: "secret"}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	newSub := newSub(namespace, "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			catalog: {
				operators: []*Operator{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", otherCatalog.Name, otherCatalog.Namespace, nil, Provides, nil, ""),
				},
			},
		},
		namespaces: []string{otherCatalog.Namespace},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, csvs, subs)
	assert.Error(t, err)
	assert.Equal(t, err.Error(), "expected exactly one operator, got 0", "did not expect to receive a resolution")
	assert.Len(t, operators, 0)
}

// Behavior: the resolver should always prefer the default channel for the subscribed bundle (unless we implement ordering for channels)
func TestSolveOperators_PreferDefaultChannelInResolution(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{Name: "community", Namespace: namespace}

	csvs := []*v1alpha1.ClusterServiceVersion{}

	const defaultChannel = "stable"
	// do not specify a channel explicitly on the subscription
	newSub := newSub(namespace, "packageA", "", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			catalog: {
				operators: []*Operator{
					// Default channel is stable in this case
					genOperator("packageA.v0.0.2", "0.0.2", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel),
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel),
				},
			},
		},
	}

	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, csvs, subs)
	assert.NoError(t, err)

	// operator should be from the default stable channel
	expected := OperatorSet{
		"packageA.v0.0.1": genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel),
	}
	require.EqualValues(t, expected, operators)
}

// Behavior: the resolver should always prefer the default channel for bundles satisfying transitive dependencies
func TestSolveOperators_PreferDefaultChannelInResolutionForTransitiveDependencies(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{Name: "community", Namespace: namespace}

	csvs := []*v1alpha1.ClusterServiceVersion{}

	newSub := newSub(namespace, "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	const defaultChannel = "stable"
	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			catalog: {
				operators: []*Operator{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, Provides, nil, apiSetToDependencies(nil, Provides), defaultChannel),
					genOperator("packageB.v0.0.1", "0.0.1", "packageB.v1", "packageB", defaultChannel, catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel),
					genOperator("packageB.v0.0.2", "0.0.2", "packageB.v0.0.1", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel),
				},
			},
		},
	}

	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, csvs, subs)
	assert.NoError(t, err)

	// operator should be from the default stable channel
	expected := OperatorSet{
		"packageA.v0.0.1": genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, Provides, nil, apiSetToDependencies(nil, Provides), defaultChannel),
		"packageB.v0.0.1": genOperator("packageB.v0.0.1", "0.0.1", "packageB.v1", "packageB", defaultChannel, catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel),
	}
	require.EqualValues(t, expected, operators)
}

func TestSolveOperators_SubscriptionlessOperatorsSatisfyDependencies(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	deps := []*api.Dependency{
		{
			Type:  "olm.gvk",
			Value: `{"group":"g","kind":"k","version":"v"}`,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageB.v1.0.0", "1.0.0", "", "packageB", "alpha", "community", "olm", Provides, nil, deps, ""),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, deps, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
		log:   logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(operators))
	expected := OperatorSet{
		"packageA.v1":     stripBundle(genOperator("packageA.v1", "", "", "packageA", "alpha", "@existing", catalog.Namespace, nil, Provides, nil, "")),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", catalog.Name, catalog.Namespace, Provides, nil, apiSetToDependencies(Provides, nil), ""),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Identifier())
	}
}

func TestSolveOperators_SubscriptionlessOperatorsCanConflict(t *testing.T) {
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: {
				key: registry.CatalogKey{
					Namespace: "olm",
					Name:      "community",
				},
				operators: []*Operator{
					genOperator("packageB.v1.0.0", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, Provides, nil, ""),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, Provides, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
		log:   logrus.New(),
	}

	_, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.Error(t, err)
}

func TestSolveOperators_PackageCannotSelfSatisfy(t *testing.T) {
	Provides1 := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Requires1 := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides2 := APISet{opregistry.APIKey{"g2", "v", "k", "ks"}: struct{}{}}
	Requires2 := APISet{opregistry.APIKey{"g2", "v", "k", "ks"}: struct{}{}}
	ProvidesBoth := Provides1.Union(Provides2)
	RequiresBoth := Requires1.Union(Requires2)

	namespace := "olm"
	catalog := registry.CatalogKey{Name: "community", Namespace: namespace}
	secondaryCatalog := registry.CatalogKey{Namespace: "olm", Name: "secondary"}

	newSub := newSub(namespace, "packageA", "stable", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			catalog: {
				key: catalog,
				operators: []*Operator{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, RequiresBoth, nil, nil, ""),
					// Despite satisfying dependencies of opA, this is not chosen because it is in the same package
					genOperator("opABC.v1.0.0", "1.0.0", "", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, ProvidesBoth, nil, ""),

					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "stable"),
					genOperator("opD.v1.0.0", "1.0.0", "", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, Provides1, nil, "stable"),
				},
			},
			secondaryCatalog: {
				key: secondaryCatalog,
				operators: []*Operator{
					genOperator("opC.v1.0.0", "1.0.0", "", "packageB", "stable", secondaryCatalog.Name, secondaryCatalog.Namespace, nil, Provides2, nil, "stable"),

					genOperator("opE.v1.0.0", "1.0.0", "", "packageC", "stable", secondaryCatalog.Name, secondaryCatalog.Namespace, nil, Provides2, nil, ""),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
		log:   logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, nil, subs)
	assert.NoError(t, err)
	expected := OperatorSet{
		"opA.v1.0.0": genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, RequiresBoth, nil, nil, ""),
		"opB.v1.0.0": genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "stable"),
		"opE.v1.0.0": genOperator("opE.v1.0.0", "1.0.0", "", "packageC", "stable", secondaryCatalog.Name, secondaryCatalog.Namespace, nil, Provides2, nil, ""),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Identifier())
	}
	assert.Equal(t, 3, len(operators))
}

func TestSolveOperators_TransferApiOwnership(t *testing.T) {
	Provides1 := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Requires1 := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides2 := APISet{opregistry.APIKey{"g2", "v", "k", "ks"}: struct{}{}}
	ProvidesBoth := Provides1.Union(Provides2)

	namespace := "olm"
	catalog := registry.CatalogKey{Name: "community", Namespace: namespace}

	phases := []struct {
		subs     []*v1alpha1.Subscription
		catalog  *CatalogSnapshot
		expected OperatorSet
	}{
		{
			subs: []*v1alpha1.Subscription{newSub(namespace, "packageB", "stable", catalog)},
			catalog: &CatalogSnapshot{
				key: catalog,
				operators: []*Operator{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, ""),
					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, Requires1, Provides2, nil, "stable"),
				},
			},
			expected: OperatorSet{
				"opA.v1.0.0": genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, ""),
				"opB.v1.0.0": genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, Requires1, Provides2, nil, "stable"),
			},
		},
		{
			// will have two existing subs after resolving once
			subs: []*v1alpha1.Subscription{
				existingSub(namespace, "opA.v1.0.0", "packageA", "stable", catalog),
				existingSub(namespace, "opB.v1.0.0", "packageB", "stable", catalog),
			},
			catalog: &CatalogSnapshot{
				key: catalog,
				operators: []*Operator{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, ""),
					genOperator("opA.v1.0.1", "1.0.1", "opA.v1.0.0", "packageA", "stable", catalog.Name, catalog.Namespace, Requires1, nil, nil, ""),
					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, Requires1, Provides2, nil, "stable"),
				},
			},
			// nothing new to do here
			expected: nil,
		},
		{
			// will have two existing subs after resolving once
			subs: []*v1alpha1.Subscription{
				existingSub(namespace, "opA.v1.0.0", "packageA", "stable", catalog),
				existingSub(namespace, "opB.v1.0.0", "packageB", "stable", catalog),
			},
			catalog: &CatalogSnapshot{
				key: catalog,
				operators: []*Operator{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, ""),
					genOperator("opA.v1.0.1", "1.0.1", "opA.v1.0.0", "packageA", "stable", catalog.Name, catalog.Namespace, Requires1, nil, nil, ""),
					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, Requires1, Provides2, nil, "stable"),
					genOperator("opB.v1.0.1", "1.0.1", "opB.v1.0.0", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, ProvidesBoth, nil, "stable"),
				},
			},
			expected: OperatorSet{
				"opA.v1.0.1": genOperator("opA.v1.0.1", "1.0.1", "opA.v1.0.0", "packageA", "stable", catalog.Name, catalog.Namespace, Requires1, nil, nil, ""),
				"opB.v1.0.1": genOperator("opB.v1.0.1", "1.0.1", "opB.v1.0.0", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, ProvidesBoth, nil, "stable"),
			},
		},
	}

	var operators OperatorSet
	for _, p := range phases {
		fakeNamespacedOperatorCache := NamespacedOperatorCache{
			snapshots: map[registry.CatalogKey]*CatalogSnapshot{
				catalog: p.catalog,
			},
		}
		satResolver := SatResolver{
			cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
			log:   logrus.New(),
		}
		csvs := make([]*v1alpha1.ClusterServiceVersion, 0)
		for _, o := range operators {
			csvs = append(csvs, existingOperator(namespace, o.Identifier(), o.Bundle().PackageName, o.Bundle().ChannelName, o.Replaces(), o.ProvidedAPIs(), o.RequiredAPIs(), nil, nil))
		}

		o, err := satResolver.SolveOperators([]string{"olm"}, csvs, p.subs)
		if p.expected != nil {
			assert.NoError(t, err)
			operators = o
		}
		for k := range p.expected {
			require.NotNil(t, o[k])
			assert.EqualValues(t, k, o[k].Identifier())
		}
		assert.Equal(t, len(p.expected), len(o))
	}
}

type FakeOperatorCache struct {
	fakedNamespacedOperatorCache NamespacedOperatorCache
}

func (f *FakeOperatorCache) Namespaced(namespaces ...string) MultiCatalogOperatorFinder {
	return &f.fakedNamespacedOperatorCache
}

func (f *FakeOperatorCache) Expire(key registry.CatalogKey) {
	return
}

func getFakeOperatorCache(fakedNamespacedOperatorCache NamespacedOperatorCache) OperatorCacheProvider {
	return &FakeOperatorCache{
		fakedNamespacedOperatorCache: fakedNamespacedOperatorCache,
	}
}

func genOperator(name, version, replaces, pkg, channel, catalogName, catalogNamespace string, requiredAPIs, providedAPIs APISet, dependencies []*api.Dependency, defaultChannel string) *Operator {
	semversion, _ := semver.Make(version)
	if len(dependencies) == 0 {
		dependencies = apiSetToDependencies(requiredAPIs, nil)
	}
	o := &Operator{
		name:     name,
		version:  &semversion,
		replaces: replaces,
		bundle: &api.Bundle{
			PackageName:  pkg,
			ChannelName:  channel,
			Dependencies: dependencies,
			Properties:   apiSetToProperties(providedAPIs, nil),
		},
		dependencies: dependencies,
		properties:   apiSetToProperties(providedAPIs, nil),
		sourceInfo: &OperatorSourceInfo{
			Catalog: registry.CatalogKey{
				Name:      catalogName,
				Namespace: catalogNamespace,
			},
			DefaultChannel: defaultChannel != "" && channel == defaultChannel,
		},
		providedAPIs: providedAPIs,
		requiredAPIs: requiredAPIs,
	}
	ensurePackageProperty(o, pkg, version)
	return o
}

func stripBundle(o *Operator) *Operator {
	o.bundle = nil
	return o
}
