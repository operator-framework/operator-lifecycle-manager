package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

const DeprecatedMigrationKey = 12

// Register this migration
func init() {
	registerMigration(DeprecatedMigrationKey, deprecatedMigration)
}

var deprecatedMigration = &Migration{
	Id: DeprecatedMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		// Purposefully forego a foreign key constraint so this table can survive operations that drop bundles and properties
		// e.g. a lossy implementation of --overwrite-latest that relies on readding all bundles in a package
		sql := `
		CREATE TABLE IF NOT EXISTS deprecated (
			operatorbundle_name TEXT PRIMARY KEY
		);
		`
		if _, err := tx.ExecContext(ctx, sql); err != nil {
			return err
		}

		initDeprecated := fmt.Sprintf(`INSERT OR REPLACE INTO deprecated(operatorbundle_name) SELECT operatorbundle_name FROM properties WHERE properties.type='%s'`, registry.DeprecatedType)
		_, err := tx.ExecContext(ctx, initDeprecated)

		return err
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TABLE deprecated`)

		return err
	},
}
