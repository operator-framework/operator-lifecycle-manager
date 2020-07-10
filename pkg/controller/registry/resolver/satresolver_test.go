package resolver

import (
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/registry"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func TestSolveOperators(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1", "0.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.1", "packageB", "alpha", "community", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(operators))
}

func TestSolveOperators_MultipleChannels(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1", "0.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "packageB", "beta", "community", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(operators))
	for _, op := range operators {
		assert.Equal(t, "alpha", op.Bundle().ChannelName)
	}
}

func TestSolveOperators_FindLatestVersion(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB", "alpha", "community", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(operators))
	for _, op := range operators {
		assert.Equal(t, "1.0.1", op.Version().String())
	}
}

func TestSolveOperators_FindLatestVersionWithDependencies(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}
	depVersion := semver.MustParseRange("1.0.1")
	opToAddVersionDeps := []VersionDependency{
		VersionDependency{
			Package: "packageC",
			Version: depVersion,
		},
		VersionDependency{
			Package: "packageD",
			Version: depVersion,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD", "alpha", "community", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))
	for _, op := range operators {
		assert.Equal(t, "1.0.1", op.Version().String())
	}
}

func TestSolveOperators_FindLatestVersionWithDependencies_ManyVersionsInCatalog(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}
	depVersion := semver.MustParseRange("1.0.1")
	opToAddVersionDeps := []VersionDependency{
		VersionDependency{
			Package: "packageC",
			Version: depVersion,
		},
		VersionDependency{
			Package: "packageD",
			Version: depVersion,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.1.0", "0.1.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.2.0", "0.2.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.3.0", "0.3.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.4.0", "0.4.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.5.0", "0.5.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.6.0", "0.6.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.7.0", "0.7.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.8.0", "0.8.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD", "alpha", "community", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))
	for _, op := range operators {
		assert.Equal(t, "1.0.1", op.Version().String())
	}
}

func TestSolveOperators_FindLatestVersionWithDependencies_LargeCatalogSet(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}
	depVersion := semver.MustParseRange("1.0.1")
	opToAddVersionDeps := []VersionDependency{
		VersionDependency{
			Package: "packageC",
			Version: depVersion,
		},
		VersionDependency{
			Package: "packageD",
			Version: depVersion,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD", "alpha", "community", "olm", nil, nil, nil),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "cat1",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD", "alpha", "community", "olm", nil, nil, nil),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "cat2",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD", "alpha", "community", "olm", nil, nil, nil),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "cat3",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "cat3", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB", "alpha", "cat3", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB", "alpha", "cat3", "olm", nil, nil, opToAddVersionDeps),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "cat4",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "cat3", "olm", nil, nil, nil),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "cat5",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "cat3", "olm", nil, nil, nil),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "cat6",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "cat3", "olm", nil, nil, nil),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "cat7",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "cat3", "olm", nil, nil, nil),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "cat8",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "cat3", "olm", nil, nil, nil),
				},
			},
			CatalogKey{
				Namespace: "ns2",
				Name:      "cat9",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "cat3", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm", "ns2"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))
	for _, op := range operators {
		assert.Equal(t, "community", op.SourceInfo().Catalog.Name)
		assert.Equal(t, "1.0.1", op.Version().String())
	}
}

func TestSolveOperators_FindLatestVersionWithNestedDependencies(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}
	depVersion := semver.MustParseRange("1.0.1")
	opToAddVersionDeps := []VersionDependency{
		VersionDependency{
			Package: "packageC",
			Version: depVersion,
		},
		VersionDependency{
			Package: "packageD",
			Version: depVersion,
		},
	}
	nestedVersionDeps := []VersionDependency{
		VersionDependency{
			Package: "packageE",
			Version: depVersion,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v0.9.0", "0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1.0.0", "1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nestedVersionDeps),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC", "alpha", "community", "olm", nil, nil, nestedVersionDeps),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageE.v1.0.1", "1.0.1", "packageE", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageE.v1.0.0", "1.0.0", "packageE", "alpha", "community", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 5, len(operators))
	for _, op := range operators {
		assert.Equal(t, "1.0.1", op.Version().String())
	}
}

func TestSolveOperators_WithDependencies(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}
	depVersion := semver.MustParseRange("0.1.0")
	opToAddVersionDeps := []VersionDependency{
		VersionDependency{
			Package: "packageC",
			Version: depVersion,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1", "0.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1", "0.1.0", "packageC", "alpha", "community", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(operators))
}

func TestSolveOperators_WithGVKDependencies(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1", "0.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, nil),
					genOperator("packageC.v1", "0.1.0", "packageC", "alpha", "community", "olm", nil, Provides, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(operators))
}

func TestSolveOperators_WithNestedGVKDependencies(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, nil),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB", "alpha", "community", "olm", Provides, nil, nil),
					genOperator("packageC.v1.0.0", "1.0.0", "packageC", "alpha", "community", "olm", Provides2, Provides, nil),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC", "alpha", "community", "olm", Provides2, Provides, nil),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD", "alpha", "community", "olm", nil, Provides2, nil),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "certified",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageC.v1.0.0", "1.0.0", "packageC", "alpha", "certified", "olm", Provides2, Provides, nil),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC", "alpha", "certified", "olm", Provides2, Provides, nil),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD", "alpha", "certified", "olm", nil, Provides2, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))
	for _, op := range operators {
		assert.Equal(t, "community", op.SourceInfo().Catalog.Name)
		assert.Equal(t, "1.0.1", op.Version().String())
	}
}

func TestSolveOperators_DependenciesMultiCatalog(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}
	depVersion := semver.MustParseRange("0.1.0")
	opToAddVersionDeps := []VersionDependency{
		VersionDependency{
			Package: "packageC",
			Version: depVersion,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1", "0.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1", "0.1.0", "packageC", "alpha", "community", "olm", nil, nil, nil),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "certified",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1", "0.0.1", "packageA", "alpha", "certified", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "packageB", "alpha", "certified", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1", "0.1.0", "packageC", "alpha", "certified", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(operators))
	for _, op := range operators {
		assert.Equal(t, "community", op.SourceInfo().Catalog.Name)
	}
}

func TestSolveOperators_IgnoreUnsatisfiableDependencies(t *testing.T) {
	APISet := APISet{registry.APIKey{"g", "v", "k", "ks"}: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := CatalogKey{"community", namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	csvs := []*v1alpha1.ClusterServiceVersion{csv}
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	opToAdd := OperatorSourceInfo{
		Package: "packageB",
		Channel: "alpha",
		Catalog: catalog,
	}
	opsToAdd := map[OperatorSourceInfo]struct{}{
		opToAdd: struct{}{},
	}
	depVersion := semver.MustParseRange("0.1.0")
	opToAddVersionDeps := []VersionDependency{
		VersionDependency{
			Package: "packageC",
			Version: depVersion,
		},
	}
	unsatisfiableVersionDeps := []VersionDependency{
		VersionDependency{
			Package: "packageD",
			Version: depVersion,
		},
	}

	fakeNamespacedOperatorCache := NamespacedOperatorCache{
		snapshots: map[CatalogKey]*CatalogSnapshot{
			CatalogKey{
				Namespace: "olm",
				Name:      "community",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1", "0.0.1", "packageA", "alpha", "community", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1", "0.1.0", "packageC", "alpha", "community", "olm", nil, nil, unsatisfiableVersionDeps),
				},
			},
			CatalogKey{
				Namespace: "olm",
				Name:      "certified",
			}: &CatalogSnapshot{
				operators: []Operator{
					genOperator("packageA.v1", "0.0.1", "packageA", "alpha", "certified", "olm", nil, nil, nil),
					genOperator("packageB.v1", "1.0.0", "packageB", "alpha", "certified", "olm", nil, nil, opToAddVersionDeps),
					genOperator("packageC.v1", "0.1.0", "packageC", "alpha", "certified", "olm", nil, nil, nil),
				},
			},
		},
	}
	satResolver := SatResolver{
		cache: getFakeOperatorCache(fakeNamespacedOperatorCache),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, csvs, subs, opsToAdd)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(operators))
	for _, op := range operators {
		if op.Identifier() == "packageC.v1" {
			assert.Equal(t, "certified", op.SourceInfo().Catalog.Name)
		} else {
			assert.Equal(t, "community", op.SourceInfo().Catalog.Name)
		}
	}
}

type FakeOperatorCache struct {
	fakedNamespacedOperatorCache NamespacedOperatorCache
}

func (f *FakeOperatorCache) Namespaced(namespaces ...string) *NamespacedOperatorCache {
	return &f.fakedNamespacedOperatorCache
}

func getFakeOperatorCache(fakedNamespacedOperatorCache NamespacedOperatorCache) OperatorCacheProvider {
	return &FakeOperatorCache{
		fakedNamespacedOperatorCache: fakedNamespacedOperatorCache,
	}
}

func genOperator(name, version, pkg, channel, catalogName, catalogNamespace string, requiredAPIs, providedAPIs APISet, versionDependencies []VersionDependency) Operator {
	semversion, _ := semver.Make(version)
	return Operator{
		name:    name,
		version: &semversion,
		bundle: &api.Bundle{
			PackageName: pkg,
			ChannelName: channel,
		},
		versionDependencies: versionDependencies,
		sourceInfo: &OperatorSourceInfo{
			Catalog: CatalogKey{
				Name:      catalogName,
				Namespace: catalogNamespace,
			},
		},
		providedAPIs: providedAPIs,
		requiredAPIs: requiredAPIs,
	}
}
