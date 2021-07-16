package migrations

import (
	"context"
	"database/sql"
)

const BundlePathPkgMigrationKey = 10

// Register this migration
func init() {
	registerMigration(BundlePathPkgMigrationKey, bundlePathPkgPropertyMigration)
}

var bundlePathPkgPropertyMigration = &Migration{
	Id: BundlePathPkgMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		updatePropertiesSql := `
		UPDATE properties
		SET operatorbundle_path = (SELECT bundlepath
							FROM operatorbundle
							WHERE operatorbundle_name = operatorbundle.name AND operatorbundle_version = operatorbundle.version)`
		_, err := tx.ExecContext(ctx, updatePropertiesSql)
		if err != nil {
			return err
		}

		return nil
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		updatePropertiesSql := `
		UPDATE properties
		SET operatorbundle_path = null
		WHERE type = "olm.package"`
		_, err := tx.ExecContext(ctx, updatePropertiesSql)
		if err != nil {
			return err
		}

		return err
	},
}
