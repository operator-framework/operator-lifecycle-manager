//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ./fakes/fake_client_provider.go . RegistryClientProvider
package resolver

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
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
	return s.BundleIterator, nil
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

	c := NewOperatorCache(rcp, logrus.New())

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
				operators := nc.Find(WithCSVName(name))
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

	c := NewOperatorCache(rcp, logrus.New())
	c.ttl = 0 // instantly stale

	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(WithCSVName("csvname")), 1)
}

func TestOperatorCacheReuse(t *testing.T) {
	rcp := RegistryClientProviderStub{}
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

	c := NewOperatorCache(rcp, logrus.New())

	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(WithCSVName("csvname")), 1)
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
		Predicate func(*Operator) bool
		Operators []*Operator
		Expected  []*Operator
	}

	for _, tt := range []tc{
		{
			Name: "nothing satisfies predicate",
			Predicate: func(*Operator) bool {
				return false
			},
			Operators: []*Operator{
				{name: "a"},
				{name: "b"},
				{name: "c"},
			},
			Expected: nil,
		},
		{
			Name: "no operators in snapshot",
			Predicate: func(*Operator) bool {
				return true
			},
			Operators: nil,
			Expected:  nil,
		},
		{
			Name: "everything satisfies predicate",
			Predicate: func(*Operator) bool {
				return true
			},
			Operators: []*Operator{
				{name: "a"},
				{name: "b"},
				{name: "c"},
			},
			Expected: []*Operator{
				{name: "a"},
				{name: "b"},
				{name: "c"},
			},
		},
		{
			Name: "some satisfy predicate",
			Predicate: func(o *Operator) bool {
				return o.name != "a"
			},
			Operators: []*Operator{
				{name: "a"},
				{name: "b"},
				{name: "c"},
			},
			Expected: []*Operator{
				{name: "b"},
				{name: "c"},
			},
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			s := CatalogSnapshot{operators: tt.Operators}
			assert.Equal(t, tt.Expected, s.Find(tt.Predicate))
		})
	}

}

func TestStripPluralRequiredAndProvidedAPIKeys(t *testing.T) {
	rcp := RegistryClientProviderStub{}
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
				Properties: apiSetToProperties(map[opregistry.APIKey]struct{}{
					{
						Group:   "g",
						Version: "v1",
						Kind:    "K",
						Plural:  "ks",
					}: {},
				}, nil),
				Dependencies: apiSetToDependencies(map[opregistry.APIKey]struct{}{
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

	c := NewOperatorCache(rcp, logrus.New())

	nc := c.Namespaced("testnamespace")
	result, err := AtLeast(1, nc.Find(ProvidingAPI(opregistry.APIKey{Group: "g", Version: "v1", Kind: "K"})))
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "K.v1.g", result[0].providedAPIs.String())
	assert.Equal(t, "K2.v2.g2", result[0].requiredAPIs.String())
}
