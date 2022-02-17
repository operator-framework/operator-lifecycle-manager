package resolver

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/operator-framework/api/pkg/constraints"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	listersv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

var testGVKKey = opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// tests can directly specify fixtures as cache entries instead of depending on this translation
func csvSnapshotOrPanic(ns string, subs []*v1alpha1.Subscription, csvs ...*v1alpha1.ClusterServiceVersion) *cache.Snapshot {
	var entries []*cache.Entry
	for _, csv := range csvs {
		entry, err := newEntryFromV1Alpha1CSV(csv)
		if err != nil {
			panic(err)
		}
		entry.SourceInfo.Catalog = cache.NewVirtualSourceKey(ns)
		for _, sub := range subs {
			if sub.Status.InstalledCSV == entry.Name {
				entry.SourceInfo.Subscription = sub
			}
		}
		entries = append(entries, entry)
	}
	return &cache.Snapshot{Entries: entries}
}

func TestSolveOperators(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	const namespace = "test-namespace"
	catalog := cache.SourceKey{Name: "test-catalog", Namespace: namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
					genOperator("packageB.v1", "1.0.1", "", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, subs)
	assert.NoError(t, err)

	expected := cache.OperatorSet{
		"packageB.v1": genOperator("packageB.v1", "1.0.1", "", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
	}
	require.EqualValues(t, expected, operators)
}

func TestSolveOperators_WithSystemConstraints(t *testing.T) {
	const namespace = "test-namespace"
	catalog := cache.SourceKey{Name: "test-catalog", Namespace: namespace}

	packageASub := newSub(namespace, "packageA", "alpha", catalog)
	packageDSub := existingSub(namespace, "packageD.v1", "packageD", "alpha", catalog)

	APISet := cache.APISet{opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: struct{}{}}

	// packageA requires an API that can be provided by B or C
	packageA := genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", catalog.Name, catalog.Namespace, APISet, nil, nil, "", false)
	packageB := genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, APISet, nil, "", false)
	packageC := genOperator("packageC.v1", "1.0.0", "", "packageC", "alpha", catalog.Name, catalog.Namespace, nil, APISet, nil, "", false)

	// Existing operators
	packageD := genOperator("packageD.v1", "1.0.0", "", "packageD", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false)
	existingPackageD := existingOperator(namespace, "packageD.v1", "packageD", "alpha", "", nil, nil, nil, nil)
	existingPackageD.Annotations = map[string]string{"operatorframework.io/properties": `{"properties":[{"type":"olm.package","value":{"packageName":"packageD","version":"1.0.0"}}]}`}

	whiteListConstraintProvider := func(whiteList ...*cache.Entry) solver.ConstraintProviderFunc {
		return func(entry *cache.Entry) ([]solver.Constraint, error) {
			for _, whiteListedEntry := range whiteList {
				if whiteListedEntry.Package() == entry.Package() &&
					whiteListedEntry.Name == entry.Name &&
					whiteListedEntry.Version == entry.Version {
					return nil, nil
				}
			}
			return []solver.Constraint{PrettyConstraint(
				solver.Prohibited(),
				fmt.Sprintf("package: %s is not white listed", entry.Package()),
			)}, nil
		}
	}

	testCases := []struct {
		title                     string
		systemConstraintsProvider solver.ConstraintProvider
		expectedOperators         cache.OperatorSet
		csvs                      []*v1alpha1.ClusterServiceVersion
		subs                      []*v1alpha1.Subscription
		snapshotEntries           []*cache.Entry
		err                       string
	}{
		{
			title:                     "No runtime constraints",
			snapshotEntries:           []*cache.Entry{packageA, packageB, packageC, packageD},
			systemConstraintsProvider: nil,
			expectedOperators:         cache.OperatorSet{"packageA.v1": packageA, "packageB.v1": packageB},
			csvs:                      nil,
			subs:                      []*v1alpha1.Subscription{packageASub},
			err:                       "",
		},
		{
			title:                     "Runtime constraints only accept packages A and C",
			snapshotEntries:           []*cache.Entry{packageA, packageB, packageC, packageD},
			systemConstraintsProvider: whiteListConstraintProvider(packageA, packageC),
			expectedOperators:         cache.OperatorSet{"packageA.v1": packageA, "packageC.v1": packageC},
			csvs:                      nil,
			subs:                      []*v1alpha1.Subscription{packageASub},
			err:                       "",
		},
		{
			title:                     "Existing packages are ignored",
			snapshotEntries:           []*cache.Entry{packageA, packageB, packageC, packageD},
			systemConstraintsProvider: whiteListConstraintProvider(packageA, packageC),
			expectedOperators:         cache.OperatorSet{"packageA.v1": packageA, "packageC.v1": packageC},
			csvs:                      []*v1alpha1.ClusterServiceVersion{existingPackageD},
			subs:                      []*v1alpha1.Subscription{packageASub, packageDSub},
			err:                       "",
		},
		{
			title:                     "Runtime constraints don't allow A",
			snapshotEntries:           []*cache.Entry{packageA, packageB, packageC, packageD},
			systemConstraintsProvider: whiteListConstraintProvider(packageB, packageC, packageD),
			expectedOperators:         nil,
			csvs:                      nil,
			subs:                      []*v1alpha1.Subscription{packageASub},
			err:                       "packageA is not white listed",
		},
	}

	for _, testCase := range testCases {
		satResolver := SatResolver{
			cache: cache.New(cache.StaticSourceProvider{
				catalog: &cache.Snapshot{
					Entries: testCase.snapshotEntries,
				},
				cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, testCase.subs, testCase.csvs...),
			}),
			log:                       logrus.New(),
			systemConstraintsProvider: testCase.systemConstraintsProvider,
		}
		operators, err := satResolver.SolveOperators([]string{namespace}, testCase.subs)

		if testCase.err != "" {
			require.Containsf(t, err.Error(), testCase.err, "Test %s failed", testCase.title)
		} else {
			require.NoErrorf(t, err, "Test %s failed", testCase.title)
		}
		require.EqualValuesf(t, testCase.expectedOperators, operators, "Test %s failed", testCase.title)
	}
}

func TestDisjointChannelGraph(t *testing.T) {
	const namespace = "test-namespace"
	catalog := cache.SourceKey{Name: "test-catalog", Namespace: namespace}

	newSub := newSub(namespace, "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.side1.v1", "0.0.1", "", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
					genOperator("packageA.side1.v2", "0.0.2", "packageA.side1.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
					genOperator("packageA.side2.v1", "1.0.0", "", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
					genOperator("packageA.side2.v2", "2.0.0", "packageA.side2.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
				},
			},
		}),
		log: logrus.New(),
	}

	_, err := satResolver.SolveOperators([]string{namespace}, subs)
	require.Error(t, err, "a unique replacement chain within a channel is required to determine the relative order between channel entries, but 2 replacement chains were found in channel \"alpha\" of package \"packageA\": packageA.side1.v2...packageA.side1.v1, packageA.side2.v2...packageA.side2.v1")
}

func TestSolveOperators_MultipleChannels(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "beta", "community", "olm", nil, nil, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	expected := cache.OperatorSet{
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
	}
	assert.Len(t, operators, 1)
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}
}

func TestSolveOperators_FindLatestVersion(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			cache.SourceKey{
				Namespace: "olm",
				Name:      "community",
			}: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v0.9.0", "0.9.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB.v0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(operators))
	for _, op := range operators {
		assert.Equal(t, "1.0.1", op.Version.String())
	}

	expected := cache.OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
	}
	assert.Equal(t, 2, len(operators))
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}
}

func TestSolveOperators_FindLatestVersionWithDependencies(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
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

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v0.9.0", "0.9.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB.v0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false),
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageD.v1.0.0", "1.0.0", "", "packageD", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageD.v1.0.1", "1.0.1", "packageD.v1.0.0", "packageD", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageD.v1.0.2", "1.0.2", "packageD.v1.0.1", "packageD", "alpha", "community", "olm", nil, nil, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))

	expected := cache.OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false),
		"packageC.v1.0.1": genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nil, "", false),
		"packageD.v1.0.1": genOperator("packageD.v1.0.1", "1.0.1", "packageD.v1.0.0", "packageD", "alpha", "community", "olm", nil, nil, nil, "", false),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Name)
	}
}

func TestSolveOperators_FindLatestVersionWithNestedDependencies(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
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

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v0.9.0", "0.9.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1.0.0", "1.0.0", "packageB.v0.9.0", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false),
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "community", "olm", nil, nil, nestedVersionDeps, "", false),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nestedVersionDeps, "", false),
					genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageE.v1.0.1", "1.0.1", "packageE.v1.0.0", "packageE", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageE.v1.0.0", "1.0.0", "", "packageE", "alpha", "community", "olm", nil, nil, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	assert.Equal(t, 5, len(operators))

	expected := cache.OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false),
		"packageC.v1.0.1": genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", nil, nil, nil, "", false),
		"packageD.v1.0.1": genOperator("packageD.v1.0.1", "1.0.1", "packageD.v1.0.0", "packageD", "alpha", "community", "olm", nil, nil, nil, "", false),
		"packageE.v1.0.1": genOperator("packageE.v1.0.1", "1.0.1", "packageE.v1.0.0", "packageE", "alpha", "community", "olm", nil, nil, nil, "", false),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Name)
	}
}

type stubCatalogSourceLister struct {
	catsrcs   []*v1alpha1.CatalogSource
	namespace string
}

func (l *stubCatalogSourceLister) List(labels.Selector) ([]*v1alpha1.CatalogSource, error) {
	if l.namespace == "" {
		return l.catsrcs, nil
	}
	var result []*v1alpha1.CatalogSource
	for _, cs := range l.catsrcs {
		if cs.Namespace == l.namespace {
			result = append(result, cs)
		}
	}
	return result, nil
}

func (l *stubCatalogSourceLister) Get(name string) (*v1alpha1.CatalogSource, error) {
	for _, cs := range l.catsrcs {
		if cs.Name == name {
			return cs, nil
		}
	}
	return nil, errors.New("stub not found")
}

func (l *stubCatalogSourceLister) CatalogSources(namespace string) listersv1alpha1.CatalogSourceNamespaceLister {
	return &stubCatalogSourceLister{
		catsrcs:   l.catsrcs,
		namespace: namespace,
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
	customCatalog := cache.SourceKey{Name: "community", Namespace: namespace}
	newSub := newSub(namespace, "packageA", "alpha", customCatalog)
	subs := []*v1alpha1.Subscription{newSub}

	ssp := cache.StaticSourceProvider{
		cache.SourceKey{Namespace: "olm", Name: "community"}: &cache.Snapshot{
			Entries: []*cache.Entry{
				genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", namespace, nil,
					nil, opToAddVersionDeps, "", false),
			},
		},
		cache.SourceKey{Namespace: "olm", Name: "community-operator"}: &cache.Snapshot{
			Entries: []*cache.Entry{
				genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community-operator",
					namespace, nil, nil, nil, "", false),
			},
		},
		cache.SourceKey{Namespace: "olm", Name: "high-priority-operator"}: &cache.Snapshot{
			Entries: []*cache.Entry{
				genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "high-priority-operator",
					namespace, nil, nil, nil, "", false),
			},
		},
	}

	satResolver := SatResolver{
		cache: cache.New(ssp, cache.WithSourcePriorityProvider(catsrcPriorityProvider{lister: &stubCatalogSourceLister{
			catsrcs: []*v1alpha1.CatalogSource{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "olm",
						Name:      "high-priority-operator",
					},
					Spec: v1alpha1.CatalogSourceSpec{
						Priority: 100,
					},
				},
			},
		}})),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	expected := cache.OperatorSet{
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm",
			nil, nil, opToAddVersionDeps, "", false),
		"packageB.v1": genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "high-priority-operator", "olm",
			nil, nil, nil, "", false),
	}
	assert.Equal(t, 2, len(operators))
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}

	// Catsrc with the same priority, ns, different name
	ssp[cache.SourceKey{
		Namespace: "olm",
		Name:      "community-operator",
	}] = &cache.Snapshot{
		Entries: []*cache.Entry{
			genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community-operator",
				namespace, nil, nil, nil, "", false),
		},
	}

	satResolver = SatResolver{
		cache: cache.New(ssp, cache.WithSourcePriorityProvider(catsrcPriorityProvider{lister: &stubCatalogSourceLister{
			catsrcs: []*v1alpha1.CatalogSource{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "olm",
						Name:      "high-priority-operator",
					},
					Spec: v1alpha1.CatalogSourceSpec{
						Priority: 100,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "olm",
						Name:      "community-operator",
					},
					Spec: v1alpha1.CatalogSourceSpec{
						Priority: 100,
					},
				},
			},
		}})),
	}

	operators, err = satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	expected = cache.OperatorSet{
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm",
			nil, nil, opToAddVersionDeps, "", false),
		"packageB.v1": genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community-operator", "olm",
			nil, nil, nil, "", false),
	}
	assert.Equal(t, 2, len(operators))
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}

	// operators from the same catalogs source should be prioritized.
	ssp[cache.SourceKey{
		Namespace: "olm",
		Name:      "community",
	}] = &cache.Snapshot{
		Entries: []*cache.Entry{
			genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", namespace, nil,
				nil, opToAddVersionDeps, "", false),
			genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community",
				namespace, nil, nil, nil, "", false),
		},
	}

	satResolver = SatResolver{
		cache: cache.New(ssp),
	}

	operators, err = satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	expected = cache.OperatorSet{
		"packageA.v1": genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm",
			nil, nil, opToAddVersionDeps, "", false),
		"packageB.v1": genOperator("packageB.v1", "0.0.1", "", "packageB", "alpha", "community", "olm",
			nil, nil, nil, "", false),
	}
	assert.Equal(t, 2, len(operators))
	for k, e := range expected {
		assert.EqualValues(t, e, operators[k])
	}
}

func TestSolveOperators_WithPackageDependencies(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub, newSub}

	opToAddVersionDeps := []*api.Dependency{
		{
			Type:  "olm.package",
			Value: `{"packageName":"packageC","version":"0.1.0"}`,
		},
	}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, nil, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(operators))

	expected := cache.OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
		"packageB.v1":     genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false),
		"packageC.v1":     genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, nil, nil, "", false),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Name)
	}
}

func TestSolveOperators_WithGVKDependencies(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	community := cache.SourceKey{Name: "community", Namespace: namespace}

	subs := []*v1alpha1.Subscription{
		existingSub(namespace, "packageA.v1", "packageA", "alpha", community),
		newSub(namespace, "packageB", "alpha", community),
	}

	deps := []*api.Dependency{
		{
			Type:  "olm.gvk",
			Value: `{"group":"g","kind":"k","version":"v"}`,
		},
	}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			community: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", Provides, nil, deps, "", false),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, Provides, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(
				namespace,
				subs,
				existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", nil, nil, nil, nil),
			),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)

	expected := cache.OperatorSet{
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", Provides, nil, deps, "", false),
		"packageC.v1": genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, Provides, nil, "", false),
	}
	assert.Equal(t, len(expected), len(operators))
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Name)
	}
}

func TestSolveOperators_WithLabelDependencies(t *testing.T) {
	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	newSub := newSub(namespace, "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	deps := []*api.Dependency{
		{
			Type:  "olm.label",
			Value: `{"label":"lts"}`,
		},
	}

	props := []*api.Property{
		{
			Type:  "olm.label",
			Value: `{"label":"lts"}`,
		},
	}

	operatorBv1 := genOperator("packageB.v1", "1.0.0", "", "packageB", "beta", "community", "olm", nil, nil, nil, "", false)
	operatorBv1.Properties = append(operatorBv1.Properties, props...)

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, deps, "", false),
					operatorBv1,
				},
			},
		}),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(operators))

	expected := cache.OperatorSet{
		"packageA":    genOperator("packageA", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, deps, "", false),
		"packageB.v1": operatorBv1,
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Name)
	}
}

func TestSolveOperators_WithUnsatisfiableLabelDependencies(t *testing.T) {
	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	newSub := newSub(namespace, "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	deps := []*api.Dependency{
		{
			Type:  "olm.label",
			Value: `{"label":"lts"}`,
		},
	}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, deps, "", false),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, nil, "", false),
				},
			},
		}),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.Error(t, err)
	assert.Equal(t, 0, len(operators))
}

func TestSolveOperators_WithNestedGVKDependencies(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
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

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			cache.SourceKey{
				Namespace: "olm",
				Name:      "community",
			}: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1.0.0", "1.0.0", "", "packageB", "alpha", "community", "olm", Provides, nil, deps, "", false),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, deps, "", false),
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "community", "olm", Provides2, Provides, deps2, "", false),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", Provides2, Provides, deps2, "", false),
					genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "community", "olm", nil, Provides2, deps2, "", false),
				},
			},
			cache.SourceKey{
				Namespace: "olm",
				Name:      "certified",
			}: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageC.v1.0.0", "1.0.0", "", "packageC", "alpha", "certified", "olm", Provides2, Provides, deps2, "", false),
					genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "certified", "olm", Provides2, Provides, deps2, "", false),
					genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "certified", "olm", nil, Provides2, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(operators))
	expected := cache.OperatorSet{
		"packageA.v1.0.1": genOperator("packageA.v1.0.1", "1.0.1", "packageA.v1", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, deps, "", false),
		"packageC.v1.0.1": genOperator("packageC.v1.0.1", "1.0.1", "packageC.v1.0.0", "packageC", "alpha", "community", "olm", Provides2, Provides, deps2, "", false),
		"packageD.v1.0.1": genOperator("packageD.v1.0.1", "1.0.1", "", "packageD", "alpha", "community", "olm", nil, Provides2, deps2, "", false),
	}
	got := []string{}
	for _, o := range operators {
		got = append(got, o.Name)
	}
	for k := range expected {
		assert.NotNil(t, operators[k], "did not find expected operator %s in results. have: %s", k, got)
		if _, ok := operators[k]; ok {
			assert.EqualValues(t, k, operators[k].Name)
		}
	}
}

type operatorGenerator struct {
	name, version                string
	replaces                     string
	pkg, channel, defaultChannel string
	catName, catNamespace        string
	requiredAPIs, providedAPIs   cache.APISet
	properties                   []*api.Property
	deprecated                   bool
}

func (g operatorGenerator) gen() *cache.Entry {
	entry := genOperator(g.name, g.version, g.replaces, g.pkg, g.channel, g.catName, g.catNamespace, g.requiredAPIs, g.providedAPIs, nil, g.defaultChannel, g.deprecated)
	entry.Properties = append(entry.Properties, g.properties...)
	return entry
}

func genOperatorsRandom(ops ...operatorGenerator) []*cache.Entry {
	entries := make([]*cache.Entry, len(ops))
	// Randomize entry order to fuzz input operators over time.
	idxs := rand.Perm(len(ops))
	for destIdx, srcIdx := range idxs {
		entries[destIdx] = ops[srcIdx].gen()
	}
	return entries
}

func TestSolveOperators_OLMConstraint_CompoundAll(t *testing.T) {
	namespace := "olm"
	csName := "community"
	catalog := cache.SourceKey{Name: csName, Namespace: namespace}

	newOperatorGens := []operatorGenerator{{
		name: "bar.v1.0.0", version: "1.0.0",
		pkg: "bar", channel: "stable",
		catName: csName, catNamespace: namespace,
		properties: []*api.Property{{
			Type: constraints.OLMConstraintType,
			Value: `{"failureMessage": "all constraint",
				"all": {"constraints": [
					{"package": {"packageName": "foo", "versionRange": ">=1.0.0"}},
					{"gvk": {"group": "g1", "version": "v1", "kind": "k1"}},
					{"gvk": {"group": "g2", "version": "v2", "kind": "k2"}}
				]}
			}`,
		}},
	}}
	dependeeOperatorGens := []operatorGenerator{{
		name: "foo.v1.0.1", version: "1.0.1",
		pkg: "foo", channel: "stable", replaces: "foo.v1.0.0",
		catName: csName, catNamespace: namespace,
		providedAPIs: cache.APISet{
			opregistry.APIKey{Group: "g1", Version: "v1", Kind: "k1"}: {},
			opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2"}: {},
			opregistry.APIKey{Group: "g3", Version: "v3", Kind: "k3"}: {},
		},
	}}

	inputs := append(dependeeOperatorGens, newOperatorGens...)

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: genOperatorsRandom(append(
					inputs,
					operatorGenerator{
						name: "foo.v0.99.0", version: "0.99.0",
						pkg: "foo", channel: "stable",
						catName: csName, catNamespace: namespace,
						providedAPIs: cache.APISet{
							opregistry.APIKey{Group: "g1", Version: "v1", Kind: "k1"}: {},
							opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2"}: {},
							opregistry.APIKey{Group: "g3", Version: "v3", Kind: "k3"}: {},
						},
					},
					operatorGenerator{
						name: "foo.v1.0.0", version: "1.0.0",
						pkg: "foo", channel: "stable", replaces: "foo.v0.99.0",
						catName: csName, catNamespace: namespace,
						providedAPIs: cache.APISet{
							opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2"}: {},
							opregistry.APIKey{Group: "g3", Version: "v3", Kind: "k3"}: {},
						},
					},
				)...),
			},
		}),
		log: logrus.New(),
	}

	newSub := newSub(namespace, "bar", "stable", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	operators, err := satResolver.SolveOperators([]string{namespace}, subs)
	require.NoError(t, err)
	assert.Equal(t, 2, len(operators))

	expected := make(cache.OperatorSet, len(inputs))
	for _, gen := range inputs {
		op := gen.gen()
		expected[op.Name] = op
	}
	for k := range expected {
		if assert.Contains(t, operators, k) {
			assert.EqualValues(t, k, operators[k].Name)
		}
	}
}

func TestSolveOperators_OLMConstraint_CompoundAny(t *testing.T) {
	namespace := "olm"
	csName := "community"
	catalog := cache.SourceKey{Name: csName, Namespace: namespace}

	newOperatorGens := []operatorGenerator{{
		name: "bar.v1.0.0", version: "1.0.0",
		pkg: "bar", channel: "stable",
		catName: csName, catNamespace: namespace,
		properties: []*api.Property{{
			Type: constraints.OLMConstraintType,
			Value: `{"failureMessage": "any constraint",
				"any": {"constraints": [
					{"gvk": {"group": "g1", "version": "v1", "kind": "k1"}},
					{"gvk": {"group": "g2", "version": "v2", "kind": "k2"}}
				]}
			}`,
		}},
	}}
	dependeeOperatorGens := []operatorGenerator{{
		name: "foo.v1.0.1", version: "1.0.1",
		pkg: "foo", channel: "stable", replaces: "foo.v1.0.0",
		catName: csName, catNamespace: namespace,
		providedAPIs: cache.APISet{
			opregistry.APIKey{Group: "g1", Version: "v1", Kind: "k1"}: {},
			opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2"}: {},
			opregistry.APIKey{Group: "g3", Version: "v3", Kind: "k3"}: {},
		},
	}}

	inputs := append(dependeeOperatorGens, newOperatorGens...)

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: genOperatorsRandom(append(
					inputs,
					operatorGenerator{
						name: "foo.v0.99.0", version: "0.99.0",
						pkg: "foo", channel: "stable",
						catName: csName, catNamespace: namespace,
						providedAPIs: cache.APISet{
							opregistry.APIKey{Group: "g0", Version: "v0", Kind: "k0"}: {},
						},
					},
					operatorGenerator{
						name: "foo.v1.0.0", version: "1.0.0",
						pkg: "foo", channel: "stable", replaces: "foo.v0.99.0",
						catName: csName, catNamespace: namespace,
						providedAPIs: cache.APISet{
							opregistry.APIKey{Group: "g1", Version: "v1", Kind: "k1"}: {},
							opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2"}: {},
						},
					},
				)...),
			},
		}),
		log: logrus.New(),
	}

	newSub := newSub(namespace, "bar", "stable", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	operators, err := satResolver.SolveOperators([]string{namespace}, subs)
	require.NoError(t, err)
	assert.Equal(t, 2, len(operators))

	expected := make(cache.OperatorSet, len(inputs))
	for _, gen := range inputs {
		op := gen.gen()
		expected[op.Name] = op
	}
	for k := range expected {
		if assert.Contains(t, operators, k) {
			assert.EqualValues(t, k, operators[k].Name)
		}
	}
}

func TestSolveOperators_OLMConstraint_CompoundNot(t *testing.T) {
	namespace := "olm"
	csName := "community"
	catalog := cache.SourceKey{Name: csName, Namespace: namespace}

	newOperatorGens := []operatorGenerator{{
		name: "bar.v1.0.0", version: "1.0.0",
		pkg: "bar", channel: "stable",
		catName: csName, catNamespace: namespace,
		properties: []*api.Property{
			{
				Type: constraints.OLMConstraintType,
				Value: `{"failureMessage": "compound not constraint",
					"all": {"constraints": [
						{"gvk": {"group": "g0", "version": "v0", "kind": "k0"}},
						{"not": {"constraints": [
							{"gvk": {"group": "g1", "version": "v1", "kind": "k1"}},
							{"gvk": {"group": "g2", "version": "v2", "kind": "k2"}}
						]}}
					]}
				}`,
			},
		},
	}}
	dependeeOperatorGens := []operatorGenerator{{
		name: "foo.v0.99.0", version: "0.99.0",
		pkg: "foo", channel: "stable",
		catName: csName, catNamespace: namespace,
		providedAPIs: cache.APISet{
			opregistry.APIKey{Group: "g0", Version: "v0", Kind: "k0"}: {},
		},
	}}

	inputs := append(dependeeOperatorGens, newOperatorGens...)

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: genOperatorsRandom(append(
					inputs,
					operatorGenerator{
						name: "foo.v1.0.0", version: "1.0.0",
						pkg: "foo", channel: "stable", replaces: "foo.v0.99.0",
						catName: csName, catNamespace: namespace,
						providedAPIs: cache.APISet{
							opregistry.APIKey{Group: "g0", Version: "v0", Kind: "k0"}: {},
							opregistry.APIKey{Group: "g1", Version: "v1", Kind: "k1"}: {},
							opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2"}: {},
						},
					},
					operatorGenerator{
						name: "foo.v1.0.1", version: "1.0.1",
						pkg: "foo", channel: "stable", replaces: "foo.v1.0.0",
						catName: csName, catNamespace: namespace,
						providedAPIs: cache.APISet{
							opregistry.APIKey{Group: "g0", Version: "v0", Kind: "k0"}: {},
							opregistry.APIKey{Group: "g1", Version: "v1", Kind: "k1"}: {},
							opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2"}: {},
							opregistry.APIKey{Group: "g3", Version: "v3", Kind: "k3"}: {},
						},
					},
				)...),
			},
		}),
		log: logrus.New(),
	}

	newSub := newSub(namespace, "bar", "stable", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	operators, err := satResolver.SolveOperators([]string{namespace}, subs)
	require.NoError(t, err)
	assert.Equal(t, 2, len(operators))

	expected := make(cache.OperatorSet, len(inputs))
	for _, gen := range inputs {
		op := gen.gen()
		expected[op.Name] = op
	}
	for k := range expected {
		if assert.Contains(t, operators, k) {
			assert.EqualValues(t, k, operators[k].Name)
		}
	}
}

func TestSolveOperators_OLMConstraint_Unknown(t *testing.T) {
	namespace := "olm"
	csName := "community"
	catalog := cache.SourceKey{Name: csName, Namespace: namespace}

	newOperatorGens := []operatorGenerator{{
		name: "bar.v1.0.0", version: "1.0.0",
		pkg: "bar", channel: "stable",
		catName: csName, catNamespace: namespace,
		properties: []*api.Property{{
			Type:  constraints.OLMConstraintType,
			Value: `{"failureMessage": "unknown constraint", "unknown": {"foo": "bar"}}`,
		}},
	}}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: genOperatorsRandom(newOperatorGens...),
			},
		}),
		log: logrus.New(),
	}

	newSub := newSub(namespace, "bar", "stable", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	_, err := satResolver.SolveOperators([]string{namespace}, subs)
	require.Error(t, err)
	require.Contains(t, err.Error(), `json: unknown field "unknown"`)
}

func TestSolveOperators_IgnoreUnsatisfiableDependencies(t *testing.T) {
	const namespace = "olm"

	Provides := cache.APISet{testGVKKey: struct{}{}}
	community := cache.SourceKey{Name: "community", Namespace: namespace}

	subs := []*v1alpha1.Subscription{
		existingSub(namespace, "packageA.v1", "packageA", "alpha", community),
		newSub(namespace, "packageB", "alpha", community),
	}

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

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			community: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "community", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "community", "olm", nil, nil, unsatisfiableVersionDeps, "", false),
				},
			},
			{Namespace: "olm", Name: "certified"}: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", "certified", "olm", nil, nil, nil, "", false),
					genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "certified", "olm", nil, nil, opToAddVersionDeps, "", false),
					genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "certified", "olm", nil, nil, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(
				namespace,
				subs,
				existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil),
			),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	expected := cache.OperatorSet{
		"packageB.v1": genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false),
		"packageC.v1": genOperator("packageC.v1", "0.1.0", "", "packageC", "alpha", "certified", "olm", nil, nil, nil, "", false),
	}
	assert.Equal(t, len(expected), len(operators))
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Name)
	}
}

// Behavior: The resolver should prefer catalogs in the same namespace as the subscription.
// It should also prefer the same catalog over global catalogs in terms of the operator cache.
func TestSolveOperators_PreferCatalogInSameNamespace(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	altNamespace := "alt-olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}
	altnsCatalog := cache.SourceKey{Name: "alt-community", Namespace: altNamespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	sub := existingSub(namespace, "packageA.v1", "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{sub}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil, "", false),
				},
			},
			altnsCatalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", altnsCatalog.Name, altnsCatalog.Namespace, nil, Provides, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, subs)
	assert.NoError(t, err)

	expected := cache.OperatorSet{
		"packageA.v0.0.1": genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil, "", false),
	}
	require.EqualValues(t, expected, operators)
}

// Behavior: The resolver should not look in catalogs not in the same namespace or the global catalog namespace when resolving the subscription.
// This test should not result in a successful resolution because the catalog fulfilling the subscription is not in the operator cache.
func TestSolveOperators_ResolveOnlyInCachedNamespaces(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}
	otherCatalog := cache.SourceKey{Name: "secret", Namespace: "secret"}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	newSub := newSub(namespace, "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", otherCatalog.Name, otherCatalog.Namespace, nil, Provides, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, subs)
	assert.Error(t, err)
	assert.Equal(t, err.Error(), "expected exactly one operator, got 0", "did not expect to receive a resolution")
	assert.Len(t, operators, 0)
}

// Behavior: the resolver should always prefer the default channel for the subscribed bundle (unless we implement ordering for channels)
func TestSolveOperators_PreferDefaultChannelInResolution(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	const defaultChannel = "stable"
	// do not specify a channel explicitly on the subscription
	newSub := newSub(namespace, "packageA", "", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					// Default channel is stable in this case
					genOperator("packageA.v0.0.2", "0.0.2", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel, false),
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel, false),
				},
			},
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, subs)
	assert.NoError(t, err)

	// operator should be from the default stable channel
	expected := cache.OperatorSet{
		"packageA.v0.0.1": genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel, false),
	}
	require.EqualValues(t, expected, operators)
}

// Behavior: the resolver should always prefer the default channel for bundles satisfying transitive dependencies
func TestSolveOperators_PreferDefaultChannelInResolutionForTransitiveDependencies(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	newSub := newSub(namespace, "packageA", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	const defaultChannel = "stable"

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, Provides, nil, apiSetToDependencies(nil, Provides), defaultChannel, false),
					genOperator("packageB.v0.0.1", "0.0.1", "packageB.v1", "packageB", defaultChannel, catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel, false),
					genOperator("packageB.v0.0.2", "0.0.2", "packageB.v0.0.1", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel, false),
				},
			},
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, subs)
	assert.NoError(t, err)

	// operator should be from the default stable channel
	expected := cache.OperatorSet{
		"packageA.v0.0.1": genOperator("packageA.v0.0.1", "0.0.1", "packageA.v1", "packageA", "alpha", catalog.Name, catalog.Namespace, Provides, nil, apiSetToDependencies(nil, Provides), defaultChannel, false),
		"packageB.v0.0.1": genOperator("packageB.v0.0.1", "0.0.1", "packageB.v1", "packageB", defaultChannel, catalog.Name, catalog.Namespace, nil, Provides, nil, defaultChannel, false),
	}
	require.EqualValues(t, expected, operators)
}

func TestSolveOperators_SubscriptionlessOperatorsSatisfyDependencies(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	deps := []*api.Dependency{
		{
			Type:  "olm.gvk",
			Value: `{"group":"g","kind":"k","version":"v"}`,
		},
	}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageB.v1.0.0", "1.0.0", "", "packageB", "alpha", "community", "olm", Provides, nil, deps, "", false),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", Provides, nil, deps, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	expected := cache.OperatorSet{
		"packageB.v1.0.1": genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", catalog.Name, catalog.Namespace, Provides, nil, apiSetToDependencies(Provides, nil), "", false),
	}
	assert.Equal(t, len(expected), len(operators))
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Name)
	}
}

func TestSolveOperators_SubscriptionlessOperatorsCanConflict(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	csv := existingOperator(namespace, "packageA.v1", "packageA", "alpha", "", Provides, nil, nil, nil)
	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageB.v1.0.0", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, Provides, nil, "", false),
					genOperator("packageB.v1.0.1", "1.0.1", "packageB.v1.0.0", "packageB", "alpha", "community", "olm", nil, Provides, nil, "", false),
				},
			},
			cache.NewVirtualSourceKey(namespace): csvSnapshotOrPanic(namespace, subs, csv),
		}),
		log: logrus.New(),
	}

	_, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.Error(t, err)
}

func TestSolveOperators_PackageCannotSelfSatisfy(t *testing.T) {
	Provides1 := cache.APISet{testGVKKey: struct{}{}}
	Requires1 := cache.APISet{testGVKKey: struct{}{}}
	Provides2 := cache.APISet{opregistry.APIKey{Group: "g2", Version: "v", Kind: "k", Plural: "ks"}: struct{}{}}
	Requires2 := cache.APISet{opregistry.APIKey{Group: "g2", Version: "v", Kind: "k", Plural: "ks"}: struct{}{}}
	ProvidesBoth := Provides1.Union(Provides2)
	RequiresBoth := Requires1.Union(Requires2)

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}
	secondaryCatalog := cache.SourceKey{Namespace: "olm", Name: "secondary"}

	newSub := newSub(namespace, "packageA", "stable", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, RequiresBoth, nil, nil, "", false),
					// Despite satisfying dependencies of opA, this is not chosen because it is in the same package
					genOperator("opABC.v1.0.0", "1.0.0", "", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, ProvidesBoth, nil, "", false),

					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "stable", false),
					genOperator("opD.v1.0.0", "1.0.0", "", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, Provides1, nil, "stable", false),
				},
			},
			secondaryCatalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("opC.v1.0.0", "1.0.0", "", "packageB", "stable", secondaryCatalog.Name, secondaryCatalog.Namespace, nil, Provides2, nil, "stable", false),

					genOperator("opE.v1.0.0", "1.0.0", "", "packageC", "stable", secondaryCatalog.Name, secondaryCatalog.Namespace, nil, Provides2, nil, "", false),
				},
			},
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	expected := cache.OperatorSet{
		"opA.v1.0.0": genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, RequiresBoth, nil, nil, "", false),
		"opB.v1.0.0": genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "stable", false),
		"opE.v1.0.0": genOperator("opE.v1.0.0", "1.0.0", "", "packageC", "stable", secondaryCatalog.Name, secondaryCatalog.Namespace, nil, Provides2, nil, "", false),
	}
	for k := range expected {
		require.NotNil(t, operators[k])
		assert.EqualValues(t, k, operators[k].Name)
	}
	assert.Equal(t, 3, len(operators))
}

func TestSolveOperators_TransferApiOwnership(t *testing.T) {
	Provides1 := cache.APISet{testGVKKey: struct{}{}}
	Requires1 := cache.APISet{testGVKKey: struct{}{}}
	Provides2 := cache.APISet{opregistry.APIKey{Group: "g2", Version: "v", Kind: "k", Plural: "ks"}: struct{}{}}
	ProvidesBoth := Provides1.Union(Provides2)

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	phases := []struct {
		subs     []*v1alpha1.Subscription
		catalog  cache.Source
		expected cache.OperatorSet
	}{
		{
			subs: []*v1alpha1.Subscription{newSub(namespace, "packageB", "stable", catalog)},
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "", false),
					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, Requires1, Provides2, nil, "stable", false),
				},
			},
			expected: cache.OperatorSet{
				"opA.v1.0.0": genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "", false),
				"opB.v1.0.0": genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, Requires1, Provides2, nil, "stable", false),
			},
		},
		{
			// will have two existing subs after resolving once
			subs: []*v1alpha1.Subscription{
				existingSub(namespace, "opA.v1.0.0", "packageA", "stable", catalog),
				existingSub(namespace, "opB.v1.0.0", "packageB", "stable", catalog),
			},
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "", false),
					genOperator("opA.v1.0.1", "1.0.1", "opA.v1.0.0", "packageA", "stable", catalog.Name, catalog.Namespace, Requires1, nil, nil, "", false),
					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, Requires1, Provides2, nil, "stable", false),
				},
			},
			// nothing new to do here
			expected: cache.OperatorSet{},
		},
		{
			// will have two existing subs after resolving once
			subs: []*v1alpha1.Subscription{
				existingSub(namespace, "opA.v1.0.0", "packageA", "stable", catalog),
				existingSub(namespace, "opB.v1.0.0", "packageB", "stable", catalog),
			},
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "", false),
					genOperator("opA.v1.0.1", "1.0.1", "opA.v1.0.0", "packageA", "stable", catalog.Name, catalog.Namespace, Requires1, nil, nil, "", false),
					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, Requires1, Provides2, nil, "stable", false),
					genOperator("opB.v1.0.1", "1.0.1", "opB.v1.0.0", "packageB", "stable", catalog.Name, catalog.Namespace, nil, ProvidesBoth, nil, "stable", false),
				},
			},
			expected: cache.OperatorSet{
				"opA.v1.0.1": genOperator("opA.v1.0.1", "1.0.1", "opA.v1.0.0", "packageA", "stable", catalog.Name, catalog.Namespace, Requires1, nil, nil, "", false),
				"opB.v1.0.1": genOperator("opB.v1.0.1", "1.0.1", "opB.v1.0.0", "packageB", "stable", catalog.Name, catalog.Namespace, nil, ProvidesBoth, nil, "stable", false),
			},
		},
	}

	var csvs fakeCSVLister
	var operators cache.OperatorSet
	for i, p := range phases {
		t.Run(fmt.Sprintf("phase %d", i+1), func(t *testing.T) {
			logger, _ := test.NewNullLogger()
			satResolver := SatResolver{
				cache: cache.New(cache.StaticSourceProvider{
					catalog: p.catalog,
					// todo: test depends on csvSource
					cache.NewVirtualSourceKey(namespace): &csvSource{
						key:       cache.NewVirtualSourceKey(namespace),
						csvLister: &csvs,
						subLister: fakeSubscriptionLister(p.subs),
						logger:    logger,
					},
				}),
				log: logger,
			}
			csvs = csvs[:0]
			for _, o := range operators {
				var pkg, channel string
				if si := o.SourceInfo; si != nil {
					pkg = si.Package
					channel = si.Channel
				}
				csvs = append(csvs, existingOperator(namespace, o.Name, pkg, channel, o.Replaces, o.ProvidedAPIs, o.RequiredAPIs, nil, nil))
			}

			var err error
			operators, err = satResolver.SolveOperators([]string{"olm"}, p.subs)
			assert.NoError(t, err)
			for k := range p.expected {
				require.NotNil(t, operators[k])
				assert.EqualValues(t, k, operators[k].Name)
			}
			assert.Equal(t, len(p.expected), len(operators))
		})
	}
}

func genOperator(name, version, replaces, pkg, channel, catalogName, catalogNamespace string, requiredAPIs, providedAPIs cache.APISet, dependencies []*api.Dependency, defaultChannel string, deprecated bool) *cache.Entry {
	semversion, _ := semver.Make(version)
	properties := apiSetToProperties(providedAPIs, nil, deprecated)
	if len(dependencies) == 0 {
		ps, err := requiredAPIsToProperties(requiredAPIs)
		if err != nil {
			panic(err)
		}
		properties = append(properties, ps...)
	} else {
		ps, err := legacyDependenciesToProperties(dependencies)
		if err != nil {
			panic(err)
		}
		properties = append(properties, ps...)
	}
	o := &cache.Entry{
		Name:       name,
		Version:    &semversion,
		Replaces:   replaces,
		Properties: properties,
		SourceInfo: &cache.OperatorSourceInfo{
			Catalog: cache.SourceKey{
				Name:      catalogName,
				Namespace: catalogNamespace,
			},
			DefaultChannel: defaultChannel != "" && channel == defaultChannel,
			Package:        pkg,
			Channel:        channel,
		},
		ProvidedAPIs: providedAPIs,
		RequiredAPIs: requiredAPIs,
	}
	EnsurePackageProperty(o, pkg, version)
	return o
}

func TestSolveOperators_WithoutDeprecated(t *testing.T) {
	catalog := cache.SourceKey{Name: "catalog", Namespace: "namespace"}

	subs := []*v1alpha1.Subscription{
		newSub(catalog.Namespace, "packageA", "alpha", catalog),
	}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("packageA.v1", "0.0.1", "", "packageA", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", true),
				},
			},
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{catalog.Namespace}, subs)
	assert.Empty(t, operators)
	assert.IsType(t, solver.NotSatisfiable{}, err)
}

func TestSolveOperatorsWithDeprecatedInnerChannelEntry(t *testing.T) {
	catalog := cache.SourceKey{Name: "catalog", Namespace: "namespace"}

	subs := []*v1alpha1.Subscription{
		newSub(catalog.Namespace, "a", "c", catalog),
	}
	logger, _ := test.NewNullLogger()
	resolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("a-1", "1.0.0", "", "a", "c", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
					genOperator("a-2", "2.0.0", "a-1", "a", "c", catalog.Name, catalog.Namespace, nil, nil, nil, "", true),
					genOperator("a-3", "3.0.0", "a-2", "a", "c", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
				},
			},
		}),
		log: logger,
	}

	operators, err := resolver.SolveOperators([]string{catalog.Namespace}, subs)
	assert.NoError(t, err)
	assert.Len(t, operators, 1)
	assert.Contains(t, operators, "a-3")
}

func TestSolveOperators_WithSkipsAndStartingCSV(t *testing.T) {
	APISet := cache.APISet{testGVKKey: struct{}{}}
	Provides := APISet

	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	newSub := newSub(namespace, "packageB", "alpha", catalog, withStartingCSV("packageB.v1"))
	subs := []*v1alpha1.Subscription{newSub}

	opToAddVersionDeps := []*api.Dependency{
		{
			Type:  "olm.gvk",
			Value: `{"group":"g","kind":"k","version":"v"}`,
		},
	}

	opB := genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false)
	opB2 := genOperator("packageB.v2", "2.0.0", "", "packageB", "alpha", "community", "olm", nil, nil, opToAddVersionDeps, "", false)
	opB2.Skips = []string{"packageB.v1"}
	op1 := genOperator("packageA.v1", "1.0.0", "", "packageA", "alpha", "community", "olm", nil, Provides, nil, "", false)
	op2 := genOperator("packageA.v2", "2.0.0", "packageA.v1", "packageA", "alpha", "community", "olm", nil, Provides, nil, "", false)
	op3 := genOperator("packageA.v3", "3.0.0", "packageA.v2", "packageA", "alpha", "community", "olm", nil, Provides, nil, "", false)
	op4 := genOperator("packageA.v4", "4.0.0", "packageA.v3", "packageA", "alpha", "community", "olm", nil, Provides, nil, "", false)
	op4.Skips = []string{"packageA.v3"}
	op5 := genOperator("packageA.v5", "5.0.0", "packageA.v4", "packageA", "alpha", "community", "olm", nil, Provides, nil, "", false)
	op5.Skips = []string{"packageA.v2", "packageA.v3", "packageA.v4"}
	op6 := genOperator("packageA.v6", "6.0.0", "packageA.v5", "packageA", "alpha", "community", "olm", nil, Provides, nil, "", false)

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					opB, opB2, op1, op2, op3, op4, op5, op6,
				},
			},
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{"olm"}, subs)
	assert.NoError(t, err)
	opB.SourceInfo.StartingCSV = "packageB.v1"
	expected := cache.OperatorSet{
		"packageB.v1": opB,
		"packageA.v6": op6,
	}
	require.EqualValues(t, expected, operators)
}

func TestSolveOperators_WithSkips(t *testing.T) {
	const namespace = "test-namespace"
	catalog := cache.SourceKey{Name: "test-catalog", Namespace: namespace}

	newSub := newSub(namespace, "packageB", "alpha", catalog)
	subs := []*v1alpha1.Subscription{newSub}

	opB := genOperator("packageB.v1", "1.0.0", "", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false)
	opB2 := genOperator("packageB.v2", "2.0.0", "", "packageB", "alpha", catalog.Name, catalog.Namespace, nil, nil, nil, "", false)
	opB2.Skips = []string{"packageB.v1"}

	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					opB, opB2,
				},
			},
		}),
		log: logrus.New(),
	}

	operators, err := satResolver.SolveOperators([]string{namespace}, subs)
	assert.NoError(t, err)
	expected := cache.OperatorSet{
		"packageB.v2": opB2,
	}
	require.EqualValues(t, expected, operators)
}

func TestSolveOperatorsWithSkipsPreventingSelection(t *testing.T) {
	const namespace = "test-namespace"
	catalog := cache.SourceKey{Name: "test-catalog", Namespace: namespace}
	gvks := cache.APISet{testGVKKey: struct{}{}}

	// Subscription candidate a-1 requires a GVK provided
	// exclusively by b-1, but b-1 is skipped by b-3 and can't be
	// chosen.
	subs := []*v1alpha1.Subscription{newSub(namespace, "a", "channel", catalog)}
	a1 := genOperator("a-1", "1.0.0", "", "a", "channel", catalog.Name, catalog.Namespace, gvks, nil, nil, "", false)
	b3 := genOperator("b-3", "3.0.0", "b-2", "b", "channel", catalog.Name, catalog.Namespace, nil, nil, nil, "", false)
	b3.Skips = []string{"b-1"}
	b2 := genOperator("b-2", "2.0.0", "b-1", "b", "channel", catalog.Name, catalog.Namespace, nil, nil, nil, "", false)
	b1 := genOperator("b-1", "1.0.0", "", "b", "channel", catalog.Name, catalog.Namespace, nil, gvks, nil, "", false)

	logger, _ := test.NewNullLogger()
	satResolver := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{a1, b3, b2, b1},
			},
		}),
		log: logger,
	}

	_, err := satResolver.SolveOperators([]string{namespace}, subs)
	assert.IsType(t, solver.NotSatisfiable{}, err)
}

func TestSolveOperatorsWithClusterServiceVersionHavingDependency(t *testing.T) {
	const namespace = "test-namespace"
	catalog := cache.SourceKey{Name: "test-catalog", Namespace: namespace}
	virtual := cache.NewVirtualSourceKey(namespace)

	subs := []*v1alpha1.Subscription{
		existingSub(namespace, "b-1", "b", "default", catalog),
	}

	log, _ := test.NewNullLogger()
	r := SatResolver{
		cache: cache.New(cache.StaticSourceProvider{
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					{
						Name:     "b-2",
						Replaces: "b-1",
						Version:  &semver.Version{},
						SourceInfo: &cache.OperatorSourceInfo{
							Package: "b",
							Channel: "default",
							Catalog: catalog,
						},
					},
				},
			},
			virtual: &cache.Snapshot{
				Entries: []*cache.Entry{
					{
						Name: "a-1",
						Properties: []*api.Property{
							{Type: "olm.package.required", Value: `{"packageName":"b","versionRange":"1.0.0"}`},
						},
						Version: &semver.Version{},
						SourceInfo: &cache.OperatorSourceInfo{
							Catalog: virtual,
						},
					},
					{
						Name: "b-1",
						Properties: []*api.Property{
							{Type: "olm.package", Value: `{"packageName":"b","version":"1.0.0"}`},
						},
						Version: &semver.Version{},
						SourceInfo: &cache.OperatorSourceInfo{
							Catalog: virtual,
						},
					},
				},
			},
		}),
		log: log,
	}

	operators, err := r.SolveOperators([]string{namespace}, subs)
	assert.NoError(t, err)
	require.Empty(t, operators)
}

func TestSortChannel(t *testing.T) {
	for _, tc := range []struct {
		Name string
		In   []*cache.Entry
		Out  []*cache.Entry
		Err  error
	}{
		{
			Name: "wrinkle-free",
			In: []*cache.Entry{
				{
					Name: "b",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
				{
					Name:     "a",
					Replaces: "b",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
			},
			Out: []*cache.Entry{
				{
					Name:     "a",
					Replaces: "b",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
				{
					Name: "b",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
			},
		},
		{
			Name: "empty",
			In:   nil,
			Out:  nil,
		},
		{
			Name: "replacement cycle",
			In: []*cache.Entry{
				{
					Name:     "a",
					Replaces: "b",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
				{
					Name:     "b",
					Replaces: "a",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
			},
			Err: errors.New(`no channel heads (entries not replaced by another entry) found in channel "channel" of package "package"`),
		},
		{
			Name: "replacement cycle",
			In: []*cache.Entry{
				{
					Name:     "a",
					Replaces: "b",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
				{
					Name:     "b",
					Replaces: "c",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
				{
					Name:     "c",
					Replaces: "b",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
			},
			Err: errors.New(`a cycle exists in the chain of replacement beginning with "a" in channel "channel" of package "package"`),
		},
		{
			Name: "skipped and replaced entry omitted",
			In: []*cache.Entry{
				{
					Name:     "a",
					Replaces: "b",
					Skips:    []string{"b"},
				},
				{
					Name: "b",
				},
			},
			Out: []*cache.Entry{
				{
					Name:     "a",
					Replaces: "b",
					Skips:    []string{"b"},
				},
			},
		},
		{
			Name: "skipped entry omitted",
			In: []*cache.Entry{
				{
					Name:     "a",
					Replaces: "b",
					Skips:    []string{"c"},
				},
				{
					Name:     "b",
					Replaces: "c",
				},
				{
					Name: "c",
				},
			},
			Out: []*cache.Entry{
				{
					Name:     "a",
					Replaces: "b",
					Skips:    []string{"c"},
				},
				{
					Name:     "b",
					Replaces: "c",
				},
			},
		},
		{
			Name: "two replaces chains",
			In: []*cache.Entry{
				{
					Name: "a",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
				{
					Name:     "b",
					Replaces: "c",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
				{
					Name: "c",
					SourceInfo: &cache.OperatorSourceInfo{
						Package: "package",
						Channel: "channel",
					},
				},
			},
			Err: errors.New(`a unique replacement chain within a channel is required to determine the relative order between channel entries, but 2 replacement chains were found in channel "channel" of package "package": a, b...c`),
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			assert := assert.New(t)
			actual, err := sortChannel(tc.In)
			if tc.Err == nil {
				assert.NoError(err)
			} else {
				assert.EqualError(err, tc.Err.Error())
			}
			assert.Equal(tc.Out, actual)
		})
	}
}

func TestSolveOperators_GenericConstraint(t *testing.T) {
	Provides1 := cache.APISet{opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: struct{}{}}
	namespace := "olm"
	catalog := cache.SourceKey{Name: "community", Namespace: namespace}

	deps1 := []*api.Dependency{
		{
			Type: "olm.constraint",
			Value: `{"failureMessage":"gvk-constraint",
				"cel":{"rule":"properties.exists(p, p.type == 'olm.gvk' && p.value == {'group': 'g', 'version': 'v', 'kind': 'k'})"}}`,
		},
	}
	deps2 := []*api.Dependency{
		{
			Type: "olm.constraint",
			Value: `{"failureMessage":"gvk2-constraint",
				"cel":{"rule":"properties.exists(p, p.type == 'olm.gvk' && p.value == {'group': 'g2', 'version': 'v', 'kind': 'k'})"}}`,
		},
	}
	deps3 := []*api.Dependency{
		{
			Type: "olm.constraint",
			Value: `{"failureMessage":"package-constraint",
				"cel":{"rule":"properties.exists(p, p.type == 'olm.package' && p.value.packageName == 'packageB' && (semver_compare(p.value.version, '1.0.1') == 0))"}}`,
		},
	}

	tests := []struct {
		name     string
		isErr    bool
		subs     []*v1alpha1.Subscription
		catalog  cache.Source
		expected cache.OperatorSet
		message  string
	}{
		{
			// generic constraint for satisfiable gvk dependency
			name:  "Generic Constraint/Satisfiable GVK Dependency",
			isErr: false,
			subs: []*v1alpha1.Subscription{
				newSub(namespace, "packageA", "stable", catalog),
			},
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, nil, deps1, "", false),
					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "stable", false),
				},
			},
			expected: cache.OperatorSet{
				"opA.v1.0.0": genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, nil, deps1, "", false),
				"opB.v1.0.0": genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "stable", false),
			},
		},
		{
			// generic constraint for NotSatisfiable gvk dependency
			name:  "Generic Constraint/NotSatisfiable GVK Dependency",
			isErr: true,
			subs: []*v1alpha1.Subscription{
				newSub(namespace, "packageA", "stable", catalog),
			},
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, nil, deps2, "", false),
					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, nil, Provides1, nil, "", false),
				},
			},
			// unable to find satisfiable gvk dependency
			// resolve into nothing
			expected: cache.OperatorSet{},
			message:  "gvk2-constraint",
		},
		{
			// generic constraint for package constraint
			name:  "Generic Constraint/Satisfiable Package Dependency",
			isErr: false,
			subs: []*v1alpha1.Subscription{
				newSub(namespace, "packageA", "stable", catalog),
			},
			catalog: &cache.Snapshot{
				Entries: []*cache.Entry{
					genOperator("opA.v1.0.0", "1.0.0", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, nil, deps3, "", false),
					genOperator("opB.v1.0.0", "1.0.0", "", "packageB", "stable", catalog.Name, catalog.Namespace, nil, nil, nil, "", false),
					genOperator("opB.v1.0.1", "1.0.1", "opB.v1.0.0", "packageB", "stable", catalog.Name, catalog.Namespace, nil, nil, nil, "stable", false),
					genOperator("opB.v1.0.2", "1.0.2", "opB.v1.0.1", "packageB", "stable", catalog.Name, catalog.Namespace, nil, nil, nil, "stable", false),
				},
			},
			expected: cache.OperatorSet{
				"opA.v1.0.0": genOperator("opA.v1.0.1", "1.0.1", "", "packageA", "stable", catalog.Name, catalog.Namespace, nil, nil, deps3, "", false),
				"opB.v1.0.1": genOperator("opB.v1.0.1", "1.0.1", "opB.v1.0.0", "packageB", "stable", catalog.Name, catalog.Namespace, nil, nil, nil, "stable", false),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			var operators cache.OperatorSet
			satResolver := SatResolver{
				cache: cache.New(cache.StaticSourceProvider{
					catalog: tt.catalog,
				}),
				log: logrus.New(),
				pc: &predicateConverter{
					celEnv: constraints.NewCelEnvironment(),
				},
			}

			operators, err = satResolver.SolveOperators([]string{namespace}, tt.subs)
			if tt.isErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.message)
			} else {
				assert.NoError(t, err)
				for k := range tt.expected {
					require.NotNil(t, operators[k])
					assert.EqualValues(t, k, operators[k].Name)
				}
			}
			assert.Equal(t, len(tt.expected), len(operators))
		})
	}
}
