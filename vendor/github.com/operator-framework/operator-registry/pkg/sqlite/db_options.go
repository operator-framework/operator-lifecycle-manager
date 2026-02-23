package sqlite

import (
	"database/sql"
)

type DbOptions struct {
	// MigratorBuilder is a function that returns a migrator instance
	MigratorBuilder func(*sql.DB) (Migrator, error)
	EnableAlpha     bool
}

type DbOption func(*DbOptions)

func defaultDBOptions() *DbOptions {
	return &DbOptions{
		MigratorBuilder: NewSQLLiteMigrator,
		EnableAlpha:     false,
	}
}

func WithMigratorBuilder(m func(loader *sql.DB) (Migrator, error)) DbOption {
	return func(o *DbOptions) {
		o.MigratorBuilder = m
	}
}

func WithEnableAlpha(enableAlpha bool) DbOption {
	return func(o *DbOptions) {
		o.EnableAlpha = enableAlpha
	}
}
