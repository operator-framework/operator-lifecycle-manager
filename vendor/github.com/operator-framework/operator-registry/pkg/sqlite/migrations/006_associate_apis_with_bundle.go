package migrations

import (
	"context"
	"database/sql"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

const AssociateApisWithBundleMigrationKey = 6

// Register this migration
func init() {
	registerMigration(AssociateApisWithBundleMigrationKey, bundleApiMigration)
}

// This migration moves the link between the provided and required apis table from the channel_entry to the
// bundle itself. This simplifies loading and minimizes changes that need to happen when a new bundle is
// inserted into an existing database.
// Before:
// api_provider: FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id),
// api_requirer: FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id),
// After:
// api_provider: FOREIGN KEY(operatorbundle_name, operatorbundle_version, operatorbundle_path) REFERENCES operatorbundle(name, version, bundlepath),
// api_requirer: FOREIGN KEY(operatorbundle_name, operatorbundle_version, operatorbundle_path) REFERENCES operatorbundle(name, version, bundlepath),

var bundleApiMigration = &Migration{
	Id: AssociateApisWithBundleMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		createNew := `
		CREATE TABLE api_provider_new (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			operatorbundle_name TEXT,
			operatorbundle_version TEXT,
			operatorbundle_path TEXT,
			FOREIGN KEY(operatorbundle_name, operatorbundle_version, operatorbundle_path) REFERENCES operatorbundle(name, version, bundlepath) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind) ON DELETE CASCADE
		);
		CREATE TABLE api_requirer_new (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			operatorbundle_name TEXT,
			operatorbundle_version TEXT,
			operatorbundle_path TEXT,
			FOREIGN KEY(operatorbundle_name, operatorbundle_version, operatorbundle_path) REFERENCES operatorbundle(name, version, bundlepath) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind) ON DELETE CASCADE
		);
-- 		these three fields are used as the target of a foreign key, so they need an index
		CREATE UNIQUE INDEX pk ON operatorbundle(name, version, bundlepath);
		`
		_, err := tx.ExecContext(ctx, createNew)
		if err != nil {
			return err
		}

		insertProvided := `INSERT INTO api_provider_new(group_name, version, kind, operatorbundle_name, operatorbundle_version, operatorbundle_path) VALUES (?, ?, ?, ?, ?, ?)`
		insertRequired := `INSERT INTO api_requirer_new(group_name, version, kind, operatorbundle_name, operatorbundle_version, operatorbundle_path) VALUES (?, ?, ?, ?, ?, ?)`

		bundleApis, err := mapBundlesToApisFromOldSchema(ctx, tx)
		if err != nil {
			return err
		}
		for bundle, apis := range bundleApis {
			for provided := range apis.provided {
				_, err := tx.ExecContext(ctx, insertProvided, provided.Group, provided.Version, provided.Kind, bundle.CsvName, bundle.Version, bundle.BundlePath)
				if err != nil {
					return err
				}
			}
			for required := range apis.required {
				_, err := tx.ExecContext(ctx, insertRequired, required.Group, required.Version, required.Kind, bundle.CsvName, bundle.Version, bundle.BundlePath)
				if err != nil {
					return err
				}
			}
		}

		renameNewAndDropOld := `
		DROP TABLE api_provider;
		DROP TABLE api_requirer;
		ALTER TABLE api_provider_new RENAME TO api_provider;
		ALTER TABLE api_requirer_new RENAME TO api_requirer;
		`
		_, err = tx.ExecContext(ctx, renameNewAndDropOld)
		if err != nil {
			return err
		}

		return err
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		createOld := `
		CREATE TABLE api_provider_old (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			channel_entry_id INTEGER,
			FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id),
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind) ON DELETE CASCADE
		);
		CREATE TABLE api_requirer_old (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			channel_entry_id INTEGER,
			FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id),
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind) ON DELETE CASCADE
		);
		`
		_, err := tx.ExecContext(ctx, createOld)
		if err != nil {
			return err
		}

		insertProvided := `INSERT INTO api_provider_old(group_name, version, kind, channel_entry_id) VALUES (?, ?, ?, ?)`
		insertRequired := `INSERT INTO api_requirer_old(group_name, version, kind, channel_entry_id) VALUES (?, ?, ?, ?)`

		entryApis, err := mapChannelEntryToApisFromNewSchema(ctx, tx)
		if err != nil {
			return err
		}
		for entry, apis := range entryApis {
			for provided := range apis.provided {
				_, err := tx.ExecContext(ctx, insertProvided, provided.Group, provided.Version, provided.Kind, entry)
				if err != nil {
					return err
				}
			}
			for required := range apis.required {
				_, err := tx.ExecContext(ctx, insertRequired, required.Group, required.Version, required.Kind, entry)
				if err != nil {
					return err
				}
			}
		}

		renameOldAndDrop := `
		DROP TABLE api_provider;
		DROP TABLE api_requirer;
		ALTER TABLE api_provider_old RENAME TO api_provider;
		ALTER TABLE api_requirer_old RENAME TO api_requirer;
		`
		_, err = tx.ExecContext(ctx, renameOldAndDrop)
		if err != nil {
			return err
		}

		return err
	},
}

type bundleKey struct {
	BundlePath sql.NullString
	Version    sql.NullString
	CsvName    sql.NullString
}

type apis struct {
	provided map[registry.APIKey]struct{}
	required map[registry.APIKey]struct{}
}

func mapBundlesToApisFromOldSchema(ctx context.Context, tx *sql.Tx) (map[bundleKey]apis, error) {
	bundles := map[bundleKey]apis{}

	providedQuery := `SELECT api_provider.group_name, api_provider.version, api_provider.kind, operatorbundle.name, operatorbundle.version, operatorbundle.bundlepath 
                       FROM api_provider
			  		   INNER JOIN channel_entry ON channel_entry.entry_id = api_provider.channel_entry_id
			           INNER JOIN operatorbundle ON operatorbundle.name = channel_entry.operatorbundle_name`

	requiredQuery := `SELECT api_requirer.group_name, api_requirer.version, api_requirer.kind, operatorbundle.name, operatorbundle.version, operatorbundle.bundlepath 
                       FROM api_requirer
			  		   INNER JOIN channel_entry ON channel_entry.entry_id = api_requirer.channel_entry_id
			           INNER JOIN operatorbundle ON operatorbundle.name = channel_entry.operatorbundle_name`

	providedRows, err := tx.QueryContext(ctx, providedQuery)
	if err != nil {
		return nil, err
	}
	for providedRows.Next() {
		var group, apiVersion, kind, name, bundleVersion, path sql.NullString

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

	requiredRows, err := tx.QueryContext(ctx, requiredQuery)
	if err != nil {
		return nil, err
	}
	for requiredRows.Next() {
		var group sql.NullString
		var apiVersion sql.NullString
		var kind sql.NullString
		var name sql.NullString
		var bundleVersion sql.NullString
		var path sql.NullString
		if err = requiredRows.Scan(&group, &apiVersion, &kind, &name, &bundleVersion, &path); err != nil {
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

		bundleApis.required[registry.APIKey{
			Group:   group.String,
			Version: apiVersion.String,
			Kind:    kind.String,
		}] = struct{}{}

		bundles[key] = bundleApis
	}

	return bundles, nil
}

func mapChannelEntryToApisFromNewSchema(ctx context.Context, tx *sql.Tx) (map[int64]apis, error) {
	bundles := map[int64]apis{}

	providedQuery := `SELECT api_provider.group_name, api_provider.version, api_provider.kind, channel_entry.entry_id 
                       FROM api_provider
                       INNER JOIN operatorbundle ON operatorbundle.name = channel_entry.operatorbundle_name
			  		   INNER JOIN channel_entry ON channel_entry.operatorbundle_name = api_provider.operatorbundle_name`

	requiredQuery := `SELECT api_requirer.group_name, api_requirer.version, api_requirer.kind, channel_entry.entry_id 
                       FROM api_requirer
			  		   INNER JOIN channel_entry ON channel_entry.operatorbundle_name = api_requirer.operatorbundle_name
			           INNER JOIN operatorbundle ON operatorbundle.name = channel_entry.operatorbundle_name`

	providedRows, err := tx.QueryContext(ctx, providedQuery)
	if err != nil {
		return nil, err
	}
	for providedRows.Next() {
		var (
			group, apiVersion, kind sql.NullString
			entryID                 sql.NullInt64
		)
		if err = providedRows.Scan(&group, &apiVersion, &kind, &entryID); err != nil {
			return nil, err
		}
		if !group.Valid || !apiVersion.Valid || !kind.Valid {
			continue
		}

		bundleApis, ok := bundles[entryID.Int64]
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

		bundles[entryID.Int64] = bundleApis
	}

	requiredRows, err := tx.QueryContext(ctx, requiredQuery)
	if err != nil {
		return nil, err
	}
	for requiredRows.Next() {
		var (
			group, apiVersion, kind sql.NullString
			entryID                 sql.NullInt64
		)
		if err = providedRows.Scan(&group, &apiVersion, &kind, &entryID); err != nil {
			return nil, err
		}
		if !group.Valid || !apiVersion.Valid || !kind.Valid {
			continue
		}

		bundleApis, ok := bundles[entryID.Int64]
		if !ok {
			bundleApis = apis{
				provided: map[registry.APIKey]struct{}{},
				required: map[registry.APIKey]struct{}{},
			}
		}

		bundleApis.required[registry.APIKey{
			Group:   group.String,
			Version: apiVersion.String,
			Kind:    kind.String,
		}] = struct{}{}

		bundles[entryID.Int64] = bundleApis
	}

	return bundles, nil
}
