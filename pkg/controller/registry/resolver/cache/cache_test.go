package cache

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				Entries: []*Entry{
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

	ssp[key] = &Snapshot{
		Entries: []*Entry{
			{Name: "v1"},
		},
		Valid: ValidOnce(),
	}
	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(CSVNamePredicate("v1")), 1)

	ssp[key] = &Snapshot{
		Entries: []*Entry{
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
		Entries: []*Entry{
			{Name: "v1"},
		},
	}
	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(CSVNamePredicate("v1")), 1)

	ssp[key] = &Snapshot{
		Entries: []*Entry{
			{Name: "v2"},
		},
	}
	require.Len(t, c.Namespaced("dummynamespace").Catalog(key).Find(CSVNamePredicate("v1")), 1)
}

func TestCatalogSnapshotValid(t *testing.T) {
	type tc struct {
		Name     string
		Snapshot *Snapshot
		Error    error
		Expected bool
	}

	for _, tt := range []tc{
		{
			Name: "invalidated",
			Snapshot: &Snapshot{
				Valid: ValidOnce(),
			},
			Error:    nil,
			Expected: false,
		},
		{
			Name:     "valid",
			Snapshot: &Snapshot{}, // valid forever
			Error:    nil,
			Expected: true,
		},
		{
			Name:     "nil snapshot and non-nil error",
			Snapshot: nil,
			Error:    errors.New(""),
			Expected: false,
		},
		{
			Name:     "non-nil snapshot and non-nil error",
			Snapshot: &Snapshot{},
			Error:    errors.New(""),
			Expected: false,
		},
		{
			Name:     "nil snapshot and nil error",
			Snapshot: nil,
			Error:    nil,
			Expected: false,
		},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			s := snapshotHeader{
				snapshot: tt.Snapshot,
				err:      tt.Error,
			}
			assert.Equal(t, tt.Expected, s.Valid())
		})
	}
}

func TestCatalogSnapshotFind(t *testing.T) {
	type tc struct {
		Name      string
		Predicate Predicate
		Operators []*Entry
		Expected  []*Entry
	}

	for _, tt := range []tc{
		{
			Name: "nothing satisfies predicate",
			Predicate: OperatorPredicateTestFunc(func(*Entry) bool {
				return false
			}),
			Operators: []*Entry{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
			Expected: nil,
		},
		{
			Name: "no operators in snapshot",
			Predicate: OperatorPredicateTestFunc(func(*Entry) bool {
				return true
			}),
			Operators: nil,
			Expected:  nil,
		},
		{
			Name: "everything satisfies predicate",
			Predicate: OperatorPredicateTestFunc(func(*Entry) bool {
				return true
			}),
			Operators: []*Entry{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
			Expected: []*Entry{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
		},
		{
			Name: "some satisfy predicate",
			Predicate: OperatorPredicateTestFunc(func(o *Entry) bool {
				return o.Name != "a"
			}),
			Operators: []*Entry{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
			Expected: []*Entry{
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
