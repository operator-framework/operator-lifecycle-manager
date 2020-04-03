package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
)

const RequiredApiMigrationKey = 3

// Register this migration
func init() {
	registerMigration(RequiredApiMigrationKey, requiredApiMigration)
}

var requiredApiMigration = &Migration{
	Id: RequiredApiMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		sql := `
		CREATE TABLE IF NOT EXISTS api_requirer (
			group_name TEXT,
			version TEXT,
			kind TEXT,
			channel_entry_id INTEGER,
			FOREIGN KEY(channel_entry_id) REFERENCES channel_entry(entry_id),
			FOREIGN KEY(group_name, version, kind) REFERENCES api(group_name, version, kind)
		);
		`
		_, err := tx.ExecContext(ctx, sql)
		if err != nil {
			return err
		}
		bundles, err := getChannelEntryBundles(ctx, tx)
		if err != nil {
			return err
		}
		for entryId, bundle := range bundles {
			if err := extractRequiredApis(ctx, tx, entryId, bundle); err != nil {
				logrus.Warnf("error backfilling required apis: %v", err)
				continue
			}
		}
		return nil
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TABLE api_requirer`)
		if err != nil {
			return err
		}

		return err
	},
}

func getChannelEntryBundles(ctx context.Context, tx *sql.Tx) (map[int64]string, error) {
	query := `SELECT DISTINCT channel_entry.entry_id, operatorbundle.name FROM channel_entry
		  INNER JOIN operatorbundle ON operatorbundle.name = channel_entry.operatorbundle_name`

	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}

	entries := map[int64]string{}

	for rows.Next() {
		var entryId sql.NullInt64
		var name sql.NullString
		if err = rows.Scan(&entryId, &name); err != nil {
			return nil, err
		}
		if !entryId.Valid || !name.Valid {
			continue
		}
		entries[entryId.Int64] = name.String
	}
	return entries, nil
}

func extractRequiredApis(ctx context.Context, tx *sql.Tx, entryId int64, name string) error {
	addAPI, err := tx.Prepare("insert or replace into api(group_name, version, kind, plural) values(?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer func() {
		if err := addAPI.Close(); err != nil {
			logrus.WithError(err).Warningf("error closing prepared statement")
		}
	}()

	addApiRequirer, err := tx.Prepare("insert into api_requirer(group_name, version, kind, channel_entry_id) values(?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer func() {
		if err := addApiRequirer.Close(); err != nil {
			logrus.WithError(err).Warningf("error closing prepared statement")
		}
	}()

	csv, err := getCSV(ctx, tx, name)
	if err != nil {
		logrus.Warnf("error backfilling required apis: %v", err)
		return err
	}

	_, requiredCRDs, err := csv.GetCustomResourceDefintions()
	for _, crd := range requiredCRDs {
		plural, group, err := SplitCRDName(crd.Name)
		if err != nil {
			return err
		}
		if _, err := addAPI.Exec(group, crd.Version, crd.Kind, plural); err != nil {
			return err
		}
		if _, err := addApiRequirer.Exec(group, crd.Version, crd.Kind, entryId); err != nil {
			return err
		}
	}

	_, requiredAPIs, err := csv.GetApiServiceDefinitions()
	for _, api := range requiredAPIs {
		if _, err := addAPI.Exec(api.Group, api.Version, api.Kind, api.Name); err != nil {
			return err
		}
		if _, err := addApiRequirer.Exec(api.Group, api.Version, api.Kind, entryId); err != nil {
			return err
		}
	}

	return nil
}

func SplitCRDName(crdName string) (plural, group string, err error) {
	pluralGroup := strings.SplitN(crdName, ".", 2)
	if len(pluralGroup) != 2 {
		err = fmt.Errorf("can't split bad CRD name %s", crdName)
		return
	}

	plural = pluralGroup[0]
	group = pluralGroup[1]
	return
}
