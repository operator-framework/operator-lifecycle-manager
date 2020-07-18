package resolver

import (
	"github.com/sirupsen/logrus"
	"testing"

	"github.com/blang/semver"
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
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.1", "", "packageB", "alpha", "community", "olm", nil, nil, nil),
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
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
		"packageB.v1": genOperator("packageB.v1", "1.0.1", "", "packageB", "alpha", "community", "olm", nil, nil, nil),
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
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "beta", "community", "olm", nil, nil, nil),
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
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil),
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
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB.v0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
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
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
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
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB.v0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageD.v1.0.0", "1.0.0", "", "packageD", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD.v1.0.0", "packageD", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageD.v1.0.2", "1.0.2", "packageD.v1.0.1", "packageD", "alpha", "community", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))

	expected := OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
		"packageC.v1.0.1": genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nil),
		"packageD.v1.0.1": genOperator("packageD.v1.0.1", "1.0.1", "packageD.v1.0.0", "packageD", "alpha", "community", "olm", nil, nil, nil),
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
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB.v0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "community", "olm", nil, nil, nestedVersionDeps),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nestedVersionDeps),
					genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageE.v1.0.1", "1.0.1", "packageE.v1.0.0", "packageE", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageE.v1.0.0", "1.0.0", "", "packageE", "alpha", "community", "olm", nil, nil, nil),
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
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
		"packageC.v1.0.1": genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nil),
		"packageD.v1.0.1": genOperator("packageD.v1.0.1", "1.0.1", "packageD.v1.0.0", "packageD", "alpha", "community", "olm", nil, nil, nil),
		"packageE.v1.0.1": genOperator("packageE.v1.0.1", "1.0.1", "packageE.v1.0.0", "packageE", "alpha", "community", "olm", nil, nil, nil),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Identifier())
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
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "0.0.1","packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "","packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, nil, nil),
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
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "","packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
		"packageC.v1": genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, nil, nil),
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
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", Provides, nil, deps),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, Provides, nil),
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
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "","packageB", "alpha", "community", "olm", Provides, nil, deps),
		"packageC.v1": genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, Provides, nil),
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
				operators: []*Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "","packageB", "alpha", "community", "olm", Provides, nil, deps),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, deps),
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "community", "olm", Provides2, Provides, deps2),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", Provides2, Provides, deps2),
					genOperator("packageD.v1.0.1", "1.0.1", "","packageD", "alpha", "community", "olm", nil, Provides2, deps2),
				},
			},
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "certified",
			}: {
				operators: []*Operator{
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "certified", "olm", Provides2, Provides, deps2),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "certified", "olm", Provides2, Provides, deps2),
					genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "certified", "olm", nil, Provides2, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))
	expected := OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, deps),
		"packageC.v1.0.1": genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", Provides2, Provides, deps2),
		"packageD.v1.0.1": genOperator("packageD.v1.0.1", "1.0.1", "","packageD", "alpha", "community", "olm", nil, Provides2, deps2),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Identifier())
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
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "","packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1", "0.1.0", "","packageC", "alpha", "community", "olm", nil, nil, unsatisfiableVersionDeps),
				},
			},
			registry.CatalogKey{
				Namespace: "olm",
				Name:      "certified",
			}: {
				operators: []*Operator{
					genOperator("packageA.v1", "0.0.1", "","packageA", "alpha", "certified", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "","packageB", "alpha", "certified", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1", "0.1.0", "","packageC", "alpha", "certified", "olm", nil, nil, nil),
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
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "","packageB", "alpha", "certified", "olm", nil, nil, opToAddVersionDeps),
		"packageC.v1": genOperator("packageC.v1", "0.1.0", "","packageC", "alpha", "certified", "olm", nil, nil, nil),
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
	newSub := newSub(namespace, "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			catalog: {
				operators: []*Operator{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil),
				},
			},
			altnsCatalog: {
				operators: []*Operator{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", altnsCatalog.Name, altnsCatalog.Namespace, nil, Provides, nil),
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
		"packageA.v0.0.1": genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil),
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
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", otherCatalog.Name, otherCatalog.Namespace, nil, Provides, nil),
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

// Behavior: the resolver should always prefer the default channel for the bundle (unless we implement ordering for channels)
// TODO
func TestSolveOperators_PreferDefaultChannelInResolution(t *testing.T) {
	t.Skip("TODO: accessing default channel....")
	APISet := APISet{opregistry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := registry.CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}

	// do not specify a channel explicitly on the subscription
	newSub := newSub(namespace, "packageA", "", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[registry.CatalogKey]*CatalogSnapshot{
			catalog: {
				operators: []*Operator{
					// Default channel is stable in this case
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil),
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides, nil),
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
		"packageA.v0.0.1": genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides, nil),
	}
	require.EqualValues(t, expected, operators)
}

type FakeOperatorCache struct {
	fakedNamespacedOperatorCache NamespacedOperatorCache
}

func (f *FakeOperatorCache) Namespaced(namespaces ...string) MultiCatalogOperatorFinder {
	return &f.fakedNamespacedOperatorCache
}

func getFakeOperatorCache(fakedNamespacedOperatorCache NamespacedOperatorCache) OperatorCacheProvider {
	return &FakeOperatorCache{
		fakedNamespacedOperatorCache: fakedNamespacedOperatorCache,
	}
}

func genOperator(name, version, replaces, pkg, channel, catalogName, catalogNamespace string, requiredAPIs, providedAPIs APISet, dependencies []*api.Dependency) *Operator {
	semversion, _ := semver.Make(version)
	return &Operator{
		name:    name,
		version: &semversion,
		replaces: replaces,
		bundle: &api.Bundle{
			PackageName: pkg,
			ChannelName: channel,
			Dependencies: dependencies,
			Properties: apiSetToProperties(providedAPIs, nil),
		},
		dependencies: dependencies,
		sourceInfo: &OperatorSourceInfo{
			Catalog: registry.CatalogKey{
				Name:      catalogName,
				Namespace: catalogNamespace,
			},
		},
		providedAPIs: providedAPIs,
		requiredAPIs: requiredAPIs,
	}
}
