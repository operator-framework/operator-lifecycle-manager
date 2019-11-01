package migrations

import (
	"context"
	"database/sql"
)

var CascadeDeleteMigrationKey = 4

// Register this migration
func init() {
	registerMigration(CascadeDeleteMigrationKey, cascadeDeleteMigration)
}

var cascadeDeleteMigration = &Migration{
	Id: CascadeDeleteMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		foreingKeyOff := `PRAGMA foreign_keys = 0`
		renameTable := func(table string) string {
			return `ALTER TABLE ` + table + ` RENAME TO ` + table + `_old;`
		}
		createNewOperatorBundleTable := `
		CREATE TABLE operatorbundle (
			name TEXT PRIMARY KEY,
			csv TEXT,
			bundle TEXT,
			bundlepath TEXT);`
		createNewPackageTable := `
		CREATE TABLE package (
			name TEXT PRIMARY KEY,
			default_channel TEXT,
			FOREIGN KEY(name, default_channel) REFERENCES channel(package_name,name) ON DELETE CASCADE
		);`
		createNewChannelTable := `
		CREATE TABLE channel (
			name TEXT,
			package_name TEXT,
			head_operatorbundle_name TEXT,
			PRIMARY KEY(name, package_name),
			FOREIGN KEY(head_operatorbundle_name) REFERENCES operatorbundle(name) ON DELETE CASCADE
		);`
		createNewChannelEntryTable := `
		CREATE TABLE channel_entry (
			entry_id INTEGER PRIMARY KEY,
			channel_name TEXT,
			package_name TEXT,
			operatorbundle_name TEXT,
			replaces INTEGER,
			depth INTEGER,
			FOREIGN KEY(replaces) REFERENCES channel_entry(entry_id) DEFERRABLE INITIALLY DEFERRED, 
			FOREIGN KEY(channel_name, package_name) REFERENCES channel(name, package_name) ON DELETE CASCADE
		);`
		createNewAPIProviderTable := `
		CREATE TABLE api_provider (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			channel_entry_id INTEGER,
			PRIMARY KEY(group_name, version, kind, channel_entry_id),
			FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id) ON DELETE CASCADE,
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind)
		);`
		createNewRelatedImageTable := `
		CREATE TABLE related_image (
			image TEXT,
     		operatorbundle_name TEXT,
     		FOREIGN KEY(operatorbundle_name) REFERENCES operatorbundle(name) ON DELETE CASCADE
		);`
		createNewAPIRequirerTable := `
		CREATE TABLE api_requirer (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			channel_entry_id INTEGER,
			PRIMARY KEY(group_name, version, kind, channel_entry_id),
			FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id) ON DELETE CASCADE,
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind)
		);`
		newTableTransfer := func(table string) string {
			return `INSERT INTO ` + table + ` SELECT * FROM "` + table + `_old"`
		}
		dropTable := func(table string) string {
			return `DROP TABLE "` + table + `_old"`
		}
		foreingKeyOn := `PRAGMA foreign_keys = 1`

		_, err := tx.ExecContext(ctx, foreingKeyOff)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`operatorbundle`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`package`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`channel`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`channel_entry`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`api_provider`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`related_image`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`api_requirer`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createNewOperatorBundleTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createNewPackageTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createNewChannelTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createNewChannelEntryTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createNewAPIProviderTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createNewRelatedImageTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createNewAPIRequirerTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, newTableTransfer(`operatorbundle`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`operatorbundle`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, newTableTransfer(`package`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`package`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, newTableTransfer(`channel`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`channel`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, newTableTransfer(`channel_entry`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`channel_entry`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, newTableTransfer(`api_provider`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`api_provider`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, newTableTransfer(`related_image`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`related_image`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, newTableTransfer(`api_requirer`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`api_requirer`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, foreingKeyOn)
		return err
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		foreingKeyOff := `PRAGMA foreign_keys = 0`
		renameTable := func(table string) string {
			return `ALTER TABLE ` + table + ` RENAME TO ` + table + `_old;`
		}
		createBackupOperatorBundleTable := `
		CREATE TABLE operatorbundle (
			name TEXT PRIMARY KEY,
			csv TEXT UNIQUE,
			bundle TEXT,
			bundlepath TEXT);`
		createBackupPackageTable := `
		CREATE TABLE IF NOT EXISTS package (
			name TEXT PRIMARY KEY,
			default_channel TEXT,
			FOREIGN KEY(name, default_channel) REFERENCES channel(package_name,name)
		);`
		createBackupChannelTable := `
		CREATE TABLE IF NOT EXISTS channel (
			name TEXT,
			package_name TEXT,
			head_operatorbundle_name TEXT,
			PRIMARY KEY(name, package_name),
			FOREIGN KEY(package_name) REFERENCES package(name),
			FOREIGN KEY(head_operatorbundle_name) REFERENCES operatorbundle(name)
		);`
		createBackupChannelEntryTable := `
		CREATE TABLE IF NOT EXISTS channel_entry (
			entry_id INTEGER PRIMARY KEY,
			channel_name TEXT,
			package_name TEXT,
			operatorbundle_name TEXT,
			replaces INTEGER,
			depth INTEGER,
			FOREIGN KEY(replaces) REFERENCES channel_entry(entry_id)  DEFERRABLE INITIALLY DEFERRED,
			FOREIGN KEY(channel_name, package_name) REFERENCES channel(name, package_name)
		);`
		createBackupAPIProviderTable := `
		CREATE TABLE IF NOT EXISTS api_provider (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			channel_entry_id INTEGER,
			FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id),
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind)
		);`
		createBackupRelatedImageTable := `
		CREATE TABLE IF NOT EXISTS related_image (
			image TEXT,
     		operatorbundle_name TEXT,
     		FOREIGN KEY(operatorbundle_name) REFERENCES operatorbundle(name)
		);`
		createBackupAPIRequirerTable := `
		CREATE TABLE IF NOT EXISTS api_requirer (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			channel_entry_id INTEGER,
			FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id),
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind)
		);`
		backupTableTransfer := func(table string) string {
			return `INSERT INTO ` + table + ` SELECT * FROM "` + table + `_old"`
		}
		dropTable := func(table string) string {
			return `DROP TABLE "` + table + `_old"`
		}

		foreingKeyOn := `PRAGMA foreign_keys = 1`

		_, err := tx.ExecContext(ctx, foreingKeyOff)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`operatorbundle`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`package`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`channel`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`channel_entry`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`api_provider`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`related_image`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, renameTable(`api_requirer`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createBackupOperatorBundleTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createBackupPackageTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createBackupChannelTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createBackupChannelEntryTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createBackupAPIProviderTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createBackupRelatedImageTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, createBackupAPIRequirerTable)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, backupTableTransfer(`operatorbundle`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`operatorbundle`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, backupTableTransfer(`package`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`package`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, backupTableTransfer(`channel`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`channel`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, backupTableTransfer(`channel_entry`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`channel_entry`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, backupTableTransfer(`api_provider`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`api_provider`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, backupTableTransfer(`related_image`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`related_image`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, backupTableTransfer(`api_requirer`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, dropTable(`api_requirer`))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, foreingKeyOn)
		return err
	},
}
