package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/golang-migrate/migrate/v4/source/file" // indirect import required by golang-migrate package
	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/pkg/sqlite/migrations"
)

type Migrator interface {
	Migrate(ctx context.Context) error
	Up(ctx context.Context, migrations migrations.Migrations) error
	Down(ctx context.Context, migrations migrations.Migrations) error
}

type SQLLiteMigrator struct {
	db              *sql.DB
	migrationsTable string
	migrations      migrations.MigrationSet
}

var _ Migrator = &SQLLiteMigrator{}

const (
	DefaultMigrationsTable = "schema_migrations"
	NilVersion             = -1
)

// NewSQLLiteMigrator returns a SQLLiteMigrator.
func NewSQLLiteMigrator(db *sql.DB) (Migrator, error) {
	return &SQLLiteMigrator{
		db:              db,
		migrationsTable: DefaultMigrationsTable,
		migrations:      migrations.All(),
	}, nil
}

// Migrate gets the current version from the database, the latest version from the migrations,
// and migrates up the the latest
func (m *SQLLiteMigrator) Migrate(ctx context.Context) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !strings.Contains(err.Error(), "transaction has already been committed") {
			logrus.WithError(err).Warnf("couldn't rollback")
		}
	}()

	version, err := m.version(ctx, tx)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return tx.Rollback()
	}
	return m.Up(ctx, m.migrations.From(version+1))
}

// Up runs a specific set of migrations.
func (m *SQLLiteMigrator) Up(ctx context.Context, migrations migrations.Migrations) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	var commitErr error
	defer func() {
		if commitErr == nil {
			return
		}
		logrus.WithError(commitErr).Warningf("tx commit failed")
		if err := tx.Rollback(); err != nil {
			logrus.WithError(err).Warningf("couldn't rollback after failed commit")
		}
	}()

	if err := m.ensureMigrationTable(ctx, tx); err != nil {
		return err
	}

	for _, migration := range migrations {
		current_version, err := m.version(ctx, tx)
		if err != nil {
			return err
		}

		if migration.Id != current_version+1 {
			return fmt.Errorf("migration applied out of order")
		}

		if err := migration.Up(ctx, tx); err != nil {
			return err
		}

		if err := m.setVersion(ctx, tx, migration.Id); err != nil {
			return err
		}
	}
	commitErr = tx.Commit()
	return commitErr
}

func (m *SQLLiteMigrator) Down(ctx context.Context, migrations migrations.Migrations) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	var commitErr error
	defer func() {
		if commitErr == nil {
			return
		}
		logrus.WithError(commitErr).Warningf("tx commit failed")
		if err := tx.Rollback(); err != nil {
			logrus.WithError(err).Warningf("couldn't rollback after failed commit")
		}
	}()
	if err := m.ensureMigrationTable(ctx, tx); err != nil {
		return err
	}

	for _, migration := range migrations {
		current_version, err := m.version(ctx, tx)
		if err != nil {
			return err
		}

		if migration.Id != current_version {
			return fmt.Errorf("migration applied out of order")
		}

		if err := migration.Down(ctx, tx); err != nil {
			return err
		}

		if err := m.setVersion(ctx, tx, migration.Id-1); err != nil {
			return err
		}
	}
	commitErr = tx.Commit()
	return commitErr
}

func (m *SQLLiteMigrator) ensureMigrationTable(ctx context.Context, tx *sql.Tx) error {
	sql := fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS %s (
		version bigint NOT NULL,
        timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`, m.migrationsTable)
	_, err := tx.ExecContext(ctx, sql)
	return err
}

func (m *SQLLiteMigrator) tableExists(tx *sql.Tx, table string) (bool, error) {
	query := `SELECT count(*)
		FROM sqlite_master
		WHERE name = ?`
	row := tx.QueryRow(query, table)

	var count int
	err := row.Scan(&count)
	if err != nil {
		return false, err
	}

	exists := count > 0
	return exists, nil
}

func (m *SQLLiteMigrator) version(ctx context.Context, tx *sql.Tx) (version int, err error) {
	tableExists, err := m.tableExists(tx, m.migrationsTable)
	if err != nil {
		return NilVersion, err
	}
	if !tableExists {
		return NilVersion, nil
	}

	query := `SELECT version FROM ` + m.migrationsTable + ` LIMIT 1`
	err = tx.QueryRowContext(ctx, query).Scan(&version)
	switch {
	case err == sql.ErrNoRows:
		return NilVersion, nil
	case err != nil:
		return NilVersion, err
	default:
		return version, nil
	}
}

func (m *SQLLiteMigrator) setVersion(ctx context.Context, tx *sql.Tx, version int) error {
	if err := m.ensureMigrationTable(ctx, tx); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM "+m.migrationsTable)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO "+m.migrationsTable+"(version) values(?)", version)
	return err
}
