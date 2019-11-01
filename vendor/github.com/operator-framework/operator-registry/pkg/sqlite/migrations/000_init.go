package migrations

import (
	"context"
	"database/sql"
)

var InitMigrationKey = 0

func init() {
	registerMigration(InitMigrationKey, initMigration)
}

var initMigration = &Migration{
	Id: InitMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		sql := `
		CREATE TABLE IF NOT EXISTS operatorbundle (
			name TEXT PRIMARY KEY,
			csv TEXT UNIQUE,
			bundle TEXT
		);
		CREATE TABLE IF NOT EXISTS package (
			name TEXT PRIMARY KEY,
			default_channel TEXT,
			FOREIGN KEY(name, default_channel) REFERENCES channel(package_name,name)
		);
		CREATE TABLE IF NOT EXISTS channel (
			name TEXT,
			package_name TEXT,
			head_operatorbundle_name TEXT,
			PRIMARY KEY(name, package_name),
			FOREIGN KEY(package_name) REFERENCES package(name),
			FOREIGN KEY(head_operatorbundle_name) REFERENCES operatorbundle(name)
		);
		CREATE TABLE IF NOT EXISTS channel_entry (
			entry_id INTEGER PRIMARY KEY,
			channel_name TEXT,
			package_name TEXT,
			operatorbundle_name TEXT,
			replaces INTEGER,
			depth INTEGER,
			FOREIGN KEY(replaces) REFERENCES channel_entry(entry_id)  DEFERRABLE INITIALLY DEFERRED,
			FOREIGN KEY(channel_name, package_name) REFERENCES channel(name, package_name)
		);
		CREATE TABLE IF NOT EXISTS api (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			plural TEXT NOT NULL,
			PRIMARY KEY(group_name, version, kind)
		);
		CREATE TABLE IF NOT EXISTS api_provider (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			channel_entry_id INTEGER,
			FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id),
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind)
		);
		`
		_, err := tx.ExecContext(ctx, sql)
		return err
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		sql := `
			DROP TABLE operatorbundle;
			DROP TABLE package;
			DROP TABLE channel;
			DROP TABLE channel_entry;
			DROP TABLE api;
			DROP TABLE api_provider;
		`
		_, err := tx.ExecContext(ctx, sql)

		return err
	},
}
