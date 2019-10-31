package migrations

import (
	"context"
	"database/sql"
)

const BundlePathMigrationKey = 2

// Register this migration
func init() {
	migrations[BundlePathMigrationKey] = bundlePathMigration
}

var bundlePathMigration = &Migration{
	Id: BundlePathMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		sql := `
		ALTER TABLE operatorbundle 
		ADD COLUMN bundlepath TEXT;
		`
		_, err := tx.ExecContext(ctx, sql)
		return err
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		foreingKeyOff := `PRAGMA foreign_keys = 0`
		createTempTable := `CREATE TABLE operatorbundle_backup (name TEXT,csv TEXT,bundle TEXT)`
		backupTargetTable := `INSERT INTO operatorbundle_backup SELECT name,csv,bundle FROM operatorbundle`
		dropTargetTable := `DROP TABLE operatorbundle`
		renameBackUpTable := `ALTER TABLE operatorbundle_backup RENAME TO operatorbundle;`
		foreingKeyOn := `PRAGMA foreign_keys = 1`
		_, err := tx.ExecContext(ctx, foreingKeyOff)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, createTempTable)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, backupTargetTable)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, dropTargetTable)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, renameBackUpTable)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, foreingKeyOn)
		return err
	},
}
