package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

type BundleStreamStub struct {
	Bundles []*api.Bundle
}

func (s *BundleStreamStub) Recv() (*api.Bundle, error) {
	if len(s.Bundles) == 0 {
		return nil, io.EOF
	}
	b := s.Bundles[0]
	s.Bundles = s.Bundles[1:]
	return b, nil
}

type RegistryClientStub struct {
	BundleIterator *client.BundleIterator

	ListBundlesError error
}

func (s *RegistryClientStub) Get() (client.Interface, error) {
	return s, nil
}

func (s *RegistryClientStub) GetBundle(ctx context.Context, packageName, channelName, csvName string) (*api.Bundle, error) {
	return nil, nil
}

func (s *RegistryClientStub) GetBundleInPackageChannel(ctx context.Context, packageName, channelName string) (*api.Bundle, error) {
	return nil, nil
}

func (s *RegistryClientStub) GetReplacementBundleInPackageChannel(ctx context.Context, currentName, packageName, channelName string) (*api.Bundle, error) {
	return nil, nil
}

func (s *RegistryClientStub) GetBundleThatProvides(ctx context.Context, group, version, kind string) (*api.Bundle, error) {
	return nil, nil
}

func (s *RegistryClientStub) ListBundles(ctx context.Context) (*client.BundleIterator, error) {
	return s.BundleIterator, s.ListBundlesError
}

func (s *RegistryClientStub) GetPackage(ctx context.Context, packageName string) (*api.Package, error) {
	return &api.Package{Name: packageName}, nil
}

func (s *RegistryClientStub) HealthCheck(ctx context.Context, reconnectTimeout time.Duration) (bool, error) {
	return false, nil
}

func (s *RegistryClientStub) Close() error {
	return nil
}

type RegistryClientProviderStub map[registry.CatalogKey]client.Interface

func (s RegistryClientProviderStub) ClientsForNamespaces(namespaces ...string) map[registry.CatalogKey]client.Interface {
	return s
}

func TestOperatorCacheConcurrency(t *testing.T) {
	const (
		NWorkers = 64
	)
	rcp := RegistryClientProviderStub{}
	catsrcLister := operatorlister.NewLister().OperatorsV1alpha1().CatalogSourceLister()
	var keys []registry.CatalogKey
	for i := 0; i < 128; i++ {
		for j := 0; j < 8; j++ {
			key := registry.CatalogKey{Namespace: strconv.Itoa(i), Name: strconv.Itoa(j)}
			keys = append(keys, key)
			rcp[key] = &RegistryClientStub{
				BundleIterator: client.NewBundleIterator(&BundleStreamStub{
					Bundles: []*api.Bundle{{
						CsvName: fmt.Sprintf("%s/%s", key.Namespace, key.Name),
						ProvidedApis: []*api.GroupVersionKind{{
							Group:   "g",
							Version: "v1",
							Kind:    "K",
							Plural:  "ks",
						}},
					}},
				}),
			}
		}
	}

	c := NewOperatorCache(rcp, logrus.New(), catsrcLister)

	errs := make(chan error)
	for w := 0; w < NWorkers; w++ {
		go func(w int) (result error) {
			defer func() { errs <- result }()

			rand := rand.New(rand.NewSource(int64(w)))
			indices := rand.Perm(len(keys))[:8]
			namespaces := make([]string, len(indices))
			for i, index := range indices {
				namespaces[i] = keys[index].Namespace
			}

			nc := c.Namespaced(namespaces...)
			for _, index := range indices {
				name := fmt.Sprintf("%s/%s", keys[index].Namespace, keys[index].Name)
				operators := nc.Find(CSVNamePredicate(name))
				if len(operators) != 1 {
					return fmt.Errorf("expected 1 operator, got %d", len(operators))
				}
			}

			return nil
		}(w)
	}

	for w := 0; w < NWorkers; w++ {
		assert.NoError(t, <-errs)
	}
}

func TestOperatorCacheExpiration(t *testing.T) {
	rcp := RegistryClientProviderStub{}
	catsrcLister := operatorlister.NewLister().OperatorsV1alpha1().CatalogSourceLister()
	key := registry.CatalogKey{Namespace: "dummynamespace", Name: "dummyname"}
	rcp[key] = &RegistryClientStub{
		BundleIterator: client.NewBundleIterator(&BundleStreamStub{
			Bundles: []*api.Bundle{{
				CsvName: "csvname",
				ProvidedApis: []*api.GroupVersionKind{{
					Group:   "g",
					Version: "v1",
					Kind:    "K",
					Plural:  "ks",
				}},
			}},
		}),
	}

	c := NewOperatorCache(rcp, logrus.New(), catsrcLister)
	c.ttl = 0 // instantly stale

	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(CSVNamePredicate("csvname")), 1)
}

func TestOperatorCacheReuse(t *testing.T) {
	rcp := RegistryClientProviderStub{}
	catsrcLister := operatorlister.NewLister().OperatorsV1alpha1().CatalogSourceLister()
	key := registry.CatalogKey{Namespace: "dummynamespace", Name: "dummyname"}
	rcp[key] = &RegistryClientStub{
		BundleIterator: client.NewBundleIterator(&BundleStreamStub{
			Bundles: []*api.Bundle{{
				CsvName: "csvname",
				ProvidedApis: []*api.GroupVersionKind{{
					Group:   "g",
					Version: "v1",
					Kind:    "K",
					Plural:  "ks",
				}},
			}},
		}),
	}

	c := NewOperatorCache(rcp, logrus.New(), catsrcLister)

	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(CSVNamePredicate("csvname")), 1)
}

func TestCatalogSnapshotExpired(t *testing.T) {
	type tc struct {
		Name     string
		Expiry   time.Time
		At       time.Time
		Expected bool
	}

	for _, tt := range []tc{
		{
			Name:     "after expiry",
			Expiry:   time.Unix(0, 1),
			At:       time.Unix(0, 2),
			Expected: true,
		},
		{
			Name:     "before expiry",
			Expiry:   time.Unix(0, 2),
			At:       time.Unix(0, 1),
			Expected: false,
		},
		{
			Name:     "at expiry",
			Expiry:   time.Unix(0, 1),
			At:       time.Unix(0, 1),
			Expected: true,
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			s := CatalogSnapshot{expiry: tt.Expiry}
			assert.Equal(t, tt.Expected, s.Expired(tt.At))
		})
	}

}

func TestCatalogSnapshotFind(t *testing.T) {
	type tc struct {
		Name      string
		Predicate OperatorPredicate
		Operators []*Operator
		Expected  []*Operator
	}

	for _, tt := range []tc{
		{
			Name: "nothing satisfies predicate",
			Predicate: OperatorPredicateTestFunc(func(*Operator) bool {
				return false
			}),
			Operators: []*Operator{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
			Expected: nil,
		},
		{
			Name: "no operators in snapshot",
			Predicate: OperatorPredicateTestFunc(func(*Operator) bool {
				return true
			}),
			Operators: nil,
			Expected:  nil,
		},
		{
			Name: "everything satisfies predicate",
			Predicate: OperatorPredicateTestFunc(func(*Operator) bool {
				return true
			}),
			Operators: []*Operator{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
			Expected: []*Operator{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
		},
		{
			Name: "some satisfy predicate",
			Predicate: OperatorPredicateTestFunc(func(o *Operator) bool {
				return o.Name != "a"
			}),
			Operators: []*Operator{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
			Expected: []*Operator{
				{Name: "b"},
				{Name: "c"},
			},
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			s := CatalogSnapshot{Operators: tt.Operators}
			assert.Equal(t, tt.Expected, s.Find(tt.Predicate))
		})
	}

}

func TestStripPluralRequiredAndProvidedAPIKeys(t *testing.T) {
	rcp := RegistryClientProviderStub{}
	catsrcLister := operatorlister.NewLister().OperatorsV1alpha1().CatalogSourceLister()
	key := registry.CatalogKey{Namespace: "testnamespace", Name: "testname"}
	rcp[key] = &RegistryClientStub{
		BundleIterator: client.NewBundleIterator(&BundleStreamStub{
			Bundles: []*api.Bundle{{
				CsvName: fmt.Sprintf("%s/%s", key.Namespace, key.Name),
				ProvidedApis: []*api.GroupVersionKind{{
					Group:   "g",
					Version: "v1",
					Kind:    "K",
					Plural:  "ks",
				}},
				RequiredApis: []*api.GroupVersionKind{{
					Group:   "g2",
					Version: "v2",
					Kind:    "K2",
					Plural:  "ks2",
				}},
				Properties: APISetToProperties(map[opregistry.APIKey]struct{}{
					{
						Group:   "g",
						Version: "v1",
						Kind:    "K",
						Plural:  "ks",
					}: {},
				}, nil, false),
				Dependencies: APISetToDependencies(map[opregistry.APIKey]struct{}{
					{
						Group:   "g2",
						Version: "v2",
						Kind:    "K2",
						Plural:  "ks2",
					}: {},
				}, nil),
			}},
		}),
	}

	c := NewOperatorCache(rcp, logrus.New(), catsrcLister)

	nc := c.Namespaced("testnamespace")
	result, err := AtLeast(1, nc.Find(ProvidingAPIPredicate(opregistry.APIKey{Group: "g", Version: "v1", Kind: "K"})))
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "K.v1.g", result[0].ProvidedAPIs.String())
	assert.Equal(t, "K2.v2.g2", result[0].RequiredAPIs.String())
}

func TestNamespaceOperatorCacheError(t *testing.T) {
	rcp := RegistryClientProviderStub{}
	catsrcLister := operatorlister.NewLister().OperatorsV1alpha1().CatalogSourceLister()
	key := registry.CatalogKey{Namespace: "dummynamespace", Name: "dummyname"}
	rcp[key] = &RegistryClientStub{
		ListBundlesError: errors.New("testing"),
	}

	logger, _ := test.NewNullLogger()
	c := NewOperatorCache(rcp, logger, catsrcLister)
	require.EqualError(t, c.Namespaced("dummynamespace").Error(), "error using catalog dummyname (in namespace dummynamespace): testing")
	if snapshot, ok := c.snapshots[key]; !ok {
		t.Fatalf("cache snapshot not found")
	} else {
		require.Zero(t, snapshot.expiry)
	}
}
