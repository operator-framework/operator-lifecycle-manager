package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

type Migration struct {
	Id   int
	Up   func(context.Context, *sql.Tx) error
	Down func(context.Context, *sql.Tx) error
}

type MigrationSet map[int]*Migration

type Migrations []*Migration

func (m Migrations) Len() int           { return len(m) }
func (m Migrations) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m Migrations) Less(i, j int) bool { return m[i].Id < m[j].Id }

var migrations MigrationSet = make(map[int]*Migration)

// From returns a set of migrations, starting at key
func (m MigrationSet) From(key int) Migrations {
	keys := make([]int, 0)
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	sorted := []*Migration{}
	for _, k := range keys {
		if k < key {
			continue
		}
		sorted = append(sorted, m[k])
	}
	return sorted
}

// To returns a set of migrations, up to and including key
func (m MigrationSet) To(key int) Migrations {
	keys := make([]int, 0)
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	sorted := []*Migration{}
	for _, k := range keys {
		if k > key {
			continue
		}
		sorted = append(sorted, m[k])
	}
	return sorted
}

// Only returns a set of one migration
func (m MigrationSet) Only(key int) Migrations {
	return []*Migration{m[key]}
}

// From returns a set of migrations, starting at key
func From(key int) Migrations {
	return migrations.From(key)
}

// To returns a set of migrations, up to and including key
func To(key int) Migrations {
	return migrations.To(key)
}

// Only returns a set of one migration
func Only(key int) Migrations {
	return migrations.Only(key)
}

// All returns the full set
func All() MigrationSet {
	return migrations
}

func registerMigration(key int, m *Migration) {
	if _, ok := migrations[key]; ok {
		panic(fmt.Sprintf("already have a migration registered with id %d", key))
	}
	if m.Id != key {
		panic(fmt.Sprintf("migration has wrong id for key. key: %d,  id: %d", key, m.Id))
	}
	migrations[key] = m
}
