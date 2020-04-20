package migrations

import (
	"context"
	"database/sql"

	"github.com/sirupsen/logrus"
)

const VersionSkipRangeMigrationKey = 5
const SkipRangeAnnotationKey = "olm.skipRange"

// Register this migration
func init() {
	registerMigration(VersionSkipRangeMigrationKey, versionSkipRangeMigration)
}

var versionSkipRangeMigration = &Migration{
	Id: VersionSkipRangeMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		sql := `
		ALTER TABLE operatorbundle 
		ADD COLUMN skiprange TEXT;

		ALTER TABLE operatorbundle 
		ADD COLUMN version TEXT;
		`
		_, err := tx.ExecContext(ctx, sql)
		if err != nil {
			return err
		}

		bundles, err := listBundles(ctx, tx)
		if err != nil {
			return err
		}
		for _, bundle := range bundles {
			if err := extractVersioning(ctx, tx, bundle); err != nil {
				logrus.Warnf("error backfilling versioning: %v", err)
				continue
			}
		}
		return err
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		foreignKeyOff := `PRAGMA foreign_keys = 0`
		createTempTable := `CREATE TABLE operatorbundle_backup (name TEXT, csv TEXT, bundle TEXT, bundlepath TEXT)`
		backupTargetTable := `INSERT INTO operatorbundle_backup SELECT name, csv, bundle, bundlepath FROM operatorbundle`
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

func extractVersioning(ctx context.Context, tx *sql.Tx, name string) error {
	addSql := `insert into operatorbundle(version, skiprange) values(?,?)`
	csv, err := getCSV(ctx, tx, name)
	if err != nil {
		logrus.Warnf("error backfilling versioning: %v", err)
		return err
	}
	skiprange, ok := csv.Annotations[SkipRangeAnnotationKey]
	if !ok {
		skiprange = ""
	}
	version, err := csv.GetVersion()
	if err != nil {
		version = ""
	}
	_, err = tx.ExecContext(ctx, addSql, version, skiprange)
	return err
}
