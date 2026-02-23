package migrations

import (
	"context"
	"database/sql"
)

const SubstitutesForMigrationKey = 11

// Register this migration
func init() {
	registerMigration(SubstitutesForMigrationKey, substitutesForPropertyMigration)
}

var substitutesForPropertyMigration = &Migration{
	Id: SubstitutesForMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		sql := `
		ALTER TABLE operatorbundle 
		ADD COLUMN substitutesfor TEXT;
		`
		_, err := tx.ExecContext(ctx, sql)
		return err
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		foreignKeyOff := `PRAGMA foreign_keys = 0`
		createTempTable := `CREATE TABLE operatorbundle_backup (name TEXT, csv TEXT, bundle TEXT, bundlepath TEXT, version TEXT, skiprange TEXT, replaces TEXT, skips TEXT)`
		backupTargetTable := `INSERT INTO operatorbundle_backup SELECT name, csv, bundle, bundlepath, version, skiprange, replaces, skips FROM operatorbundle`
		dropTargetTable := `DROP TABLE operatorbundle`
		renameBackUpTable := `ALTER TABLE operatorbundle_backup RENAME TO operatorbundle;`
		foreignKeyOn := `PRAGMA foreign_keys = 1`
		_, err := tx.ExecContext(ctx, foreignKeyOff)
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
		_, err = tx.ExecContext(ctx, foreignKeyOn)
		return err
	},
}
