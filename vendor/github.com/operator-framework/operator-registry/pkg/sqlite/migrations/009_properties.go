package migrations

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

const PropertiesMigrationKey = 9

// Register this migration
func init() {
	registerMigration(PropertiesMigrationKey, propertiesMigration)
}

var propertiesMigration = &Migration{
	Id: PropertiesMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		sql := `
		CREATE TABLE IF NOT EXISTS properties (
			type TEXT,
			value TEXT,
			operatorbundle_name TEXT,
			operatorbundle_version TEXT,
			operatorbundle_path TEXT,
			FOREIGN KEY(operatorbundle_name, operatorbundle_version, operatorbundle_path) REFERENCES operatorbundle(name, version, bundlepath) ON DELETE CASCADE
		);
		`
		_, err := tx.ExecContext(ctx, sql)
		if err != nil {
			return err
		}

		insertProperty := `INSERT INTO properties(type, value, operatorbundle_name, operatorbundle_version, operatorbundle_path) VALUES (?, ?, ?, ?, ?)`

		bundleApis, err := getProvidedAPIs(ctx, tx)
		if err != nil {
			return err
		}
		for bundle, apis := range bundleApis {
			pkg, err := getPackageForBundle(ctx, bundle.CsvName.String, tx)
			if err != nil {
				return err
			}
			valueMap := map[string]string{
				"packageName": pkg,
				"version":     bundle.Version.String,
			}
			value, err := json.Marshal(valueMap)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, insertProperty, registry.PackageType, value, bundle.CsvName, bundle.Version, bundle.BundlePath)
			if err != nil {
				return err
			}

			for provided := range apis.provided {
				valueMap := map[string]string{
					"group":   provided.Group,
					"version": provided.Version,
					"kind":    provided.Kind,
				}
				value, err := json.Marshal(valueMap)
				if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, insertProperty, registry.GVKType, value, bundle.CsvName, bundle.Version, bundle.BundlePath)
				if err != nil {
					return err
				}
			}
		}

		// update the serialized value to omit the dependency type
		updateDependencySql := `
		UPDATE dependencies
		SET value = (SELECT json_remove(value, "$.type")
					FROM dependencies
					WHERE operatorbundle_name=dependencies.operatorbundle_name)`
		_, err = tx.ExecContext(ctx, updateDependencySql)
		if err != nil {
			return err
		}

		return nil
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TABLE properties`)
		if err != nil {
			return err
		}

		return err
	},
}

func getPackageForBundle(ctx context.Context, name string, tx *sql.Tx) (string, error) {
	packageQuery := `SELECT DISTINCT package_name FROM channel_entry WHERE channel_entry.operatorbundle_name=?`
	packageRows, err := tx.QueryContext(ctx, packageQuery, name)
	if err != nil {
		return "", err
	}
	for packageRows.Next() {
		var pkg sql.NullString
		if err = packageRows.Scan(&pkg); err != nil {
			return "", err
		}
		if !pkg.Valid {
			return "", err
		}
		return pkg.String, nil
	}
	return "", err
}

func getProvidedAPIs(ctx context.Context, tx *sql.Tx) (map[bundleKey]apis, error) {
	bundles := map[bundleKey]apis{}

	providedQuery := `SELECT group_name, version, kind, operatorbundle_name, operatorbundle_version, operatorbundle_path
  					  FROM api_provider`

	providedRows, err := tx.QueryContext(ctx, providedQuery)
	if err != nil {
		return nil, err
	}
	for providedRows.Next() {
		var group sql.NullString
		var apiVersion sql.NullString
		var kind sql.NullString
		var name sql.NullString
		var bundleVersion sql.NullString
		var path sql.NullString
		if err = providedRows.Scan(&group, &apiVersion, &kind, &name, &bundleVersion, &path); err != nil {
			return nil, err
		}
		if !group.Valid || !apiVersion.Valid || !kind.Valid || !name.Valid {
			continue
		}
		key := bundleKey{
			BundlePath: path,
			Version:    bundleVersion,
			CsvName:    name,
		}
		bundleApis, ok := bundles[key]
		if !ok {
			bundleApis = apis{
				provided: map[registry.APIKey]struct{}{},
				required: map[registry.APIKey]struct{}{},
			}
		}

		bundleApis.provided[registry.APIKey{
			Group:   group.String,
			Version: apiVersion.String,
			Kind:    kind.String,
		}] = struct{}{}

		bundles[key] = bundleApis
	}

	return bundles, nil
}
