package cache

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/operator-registry/pkg/api"
)

func TestOperatorCacheConcurrency(t *testing.T) {
	const (
		NWorkers = 64
	)

	sp := make(StaticSourceProvider)
	var keys []SourceKey
	for i := 0; i < 128; i++ {
		for j := 0; j < 8; j++ {
			key := SourceKey{Namespace: strconv.Itoa(i), Name: strconv.Itoa(j)}
			keys = append(keys, key)
			sp[key] = &Snapshot{
				Entries: []*Operator{
					{Name: fmt.Sprintf("%s/%s", key.Namespace, key.Name)},
				},
			}
		}
	}

	c := New(sp)

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
	key := SourceKey{Namespace: "dummynamespace", Name: "dummyname"}
	ssp := make(StaticSourceProvider)
	c := New(ssp)
	c.ttl = 0 // instantly stale

	ssp[key] = &Snapshot{
		Entries: []*Operator{
			{Name: "v1"},
		},
	}
	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(CSVNamePredicate("v1")), 1)

	ssp[key] = &Snapshot{
		Entries: []*Operator{
			{Name: "v2"},
		},
	}
	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(CSVNamePredicate("v1")), 0)
}

func TestOperatorCacheReuse(t *testing.T) {
	key := SourceKey{Namespace: "dummynamespace", Name: "dummyname"}
	ssp := make(StaticSourceProvider)
	c := New(ssp)

	ssp[key] = &Snapshot{
		Entries: []*Operator{
			{Name: "v1"},
		},
	}
	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(CSVNamePredicate("v1")), 1)

	ssp[key] = &Snapshot{
		Entries: []*Operator{
			{Name: "v2"},
		},
	}
	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(CSVNamePredicate("v1")), 1)
}

func TestCatalogSnapshotValid(t *testing.T) {
	type tc struct {
		Name     string
		Expiry   time.Time
		Snapshot *Snapshot
		Error    error
		At       time.Time
		Expected bool
	}

	for _, tt := range []tc{
		{
			Name:     "after expiry",
			Expiry:   time.Unix(0, 1),
			Snapshot: &Snapshot{},
			Error:    nil,
			At:       time.Unix(0, 2),
			Expected: false,
		},
		{
			Name:     "before expiry",
			Expiry:   time.Unix(0, 2),
			Snapshot: &Snapshot{},
			Error:    nil,
			At:       time.Unix(0, 1),
			Expected: true,
		},
		{
			Name:     "nil snapshot",
			Expiry:   time.Unix(0, 2),
			Snapshot: nil,
			Error:    errors.New(""),
			At:       time.Unix(0, 1),
			Expected: false,
		},
		{
			Name:     "non-nil error",
			Expiry:   time.Unix(0, 2),
			Snapshot: &Snapshot{},
			Error:    errors.New(""),
			At:       time.Unix(0, 1),
			Expected: false,
		},
		{
			Name:     "at expiry",
			Expiry:   time.Unix(0, 1),
			Snapshot: &Snapshot{},
			Error:    nil,
			At:       time.Unix(0, 1),
			Expected: false,
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			s := snapshotHeader{
				expiry:   tt.Expiry,
				snapshot: tt.Snapshot,
				err:      tt.Error,
			}
			assert.Equal(t, tt.Expected, s.Valid(tt.At))
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
			s := snapshotHeader{snapshot: &Snapshot{Entries: tt.Operators}}
			assert.Equal(t, tt.Expected, s.Find(tt.Predicate))
		})
	}

}

func TestNewOperatorFromBundleStripsPluralRequiredAndProvidedAPIKeys(t *testing.T) {
	key := SourceKey{Namespace: "testnamespace", Name: "testname"}
	o, err := NewOperatorFromBundle(&api.Bundle{
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
	}, "", key, "")

	assert.NoError(t, err)
	assert.Equal(t, "K.v1.g", o.ProvidedAPIs.String())
	assert.Equal(t, "K2.v2.g2", o.RequiredAPIs.String())
}

type ErrorSource struct {
	Error error
}

func (s ErrorSource) Snapshot(context.Context) (*Snapshot, error) {
	return nil, s.Error
}

func TestNamespaceOperatorCacheError(t *testing.T) {
	key := SourceKey{Namespace: "dummynamespace", Name: "dummyname"}
	c := New(StaticSourceProvider{
		key: ErrorSource{Error: errors.New("testing")},
	})

	require.EqualError(t, c.Namespaced("dummynamespace").Error(), "error using catalog dummyname (in namespace dummynamespace): testing")
}
