package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

type SQLLoader struct {
	db *sql.DB
}

var _ registry.Load = &SQLLoader{}

func NewSQLLiteLoader(outFilename string) (*SQLLoader, error) {
	db, err := sql.Open("sqlite3", outFilename) // TODO: ?immutable=true
	if err != nil {
		return nil, err
	}

	createTable := `
	CREATE TABLE IF NOT EXISTS operatorbundle (
		name TEXT PRIMARY KEY,  
		csv TEXT UNIQUE, 
		bundle TEXT
	);
	CREATE TABLE IF NOT EXISTS package (
		name TEXT PRIMARY KEY,
		default_channel TEXT,
		FOREIGN KEY(default_channel) REFERENCES channel(name)
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
		FOREIGN KEY(channel_name) REFERENCES channel(name),
		FOREIGN KEY(package_name) REFERENCES channel(package_name),
		FOREIGN KEY(operatorbundle_name) REFERENCES operatorbundle(name)
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
	CREATE INDEX IF NOT EXISTS replaces ON operatorbundle(json_extract(csv, '$.spec.replaces'));
	`

	if _, err = db.Exec(createTable); err != nil {
		return nil, err
	}
	return &SQLLoader{db}, nil
}

func (s *SQLLoader) AddOperatorBundle(bundle *registry.Bundle) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("insert into operatorbundle(name, csv, bundle) values(?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	csvName, csvBytes, bundleBytes, err := bundle.Serialize()
	if err != nil {
		return err
	}

	if _, err := stmt.Exec(csvName, csvBytes, bundleBytes); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SQLLoader) AddPackageChannels(manifest registry.PackageManifest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	addPackage, err := tx.Prepare("insert into package(name) values(?)")
	if err != nil {
		return err
	}
	defer addPackage.Close()

	addDefaultChannel, err := tx.Prepare("update package set default_channel = ? where name = ?")
	if err != nil {
		return err
	}
	defer addPackage.Close()

	if _, err := addPackage.Exec(manifest.PackageName); err != nil {
		return err
	}

	addChannel, err := tx.Prepare("insert into channel(name, package_name, head_operatorbundle_name) values(?, ?, ?)")
	if err != nil {
		return err
	}
	defer addChannel.Close()

	for _, c := range manifest.Channels {
		if _, err := addChannel.Exec(c.Name, manifest.PackageName, c.CurrentCSVName); err != nil {
			return err
		}
		if c.IsDefaultChannel(manifest) {
			if _, err := addDefaultChannel.Exec(c.Name, manifest.PackageName); err != nil {
				return err
			}
		}
	}

	addChannelEntry, err := tx.Prepare("insert into channel_entry(channel_name, package_name, operatorbundle_name, depth) values(?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addChannelEntry.Close()

	getReplaces, err := tx.Prepare(`
	 SELECT DISTINCT json_extract(operatorbundle.csv, '$.spec.replaces')
	 FROM operatorbundle,json_tree(operatorbundle.csv)
	 WHERE operatorbundle.name IS ?
	`)
	defer getReplaces.Close()

	addReplaces, err := tx.Prepare("update channel_entry set replaces = ? where entry_id = ?")
	if err != nil {
		return err
	}
	defer addReplaces.Close()

	for _, c := range manifest.Channels {
		res, err := addChannelEntry.Exec(c.Name, manifest.PackageName, c.CurrentCSVName, 0)
		if err != nil {
			return err
		}
		currentID, err := res.LastInsertId()
		if err != nil {
			return err
		}

		channelEntryCSVName := c.CurrentCSVName
		depth := 1
		for {
			rows, err := getReplaces.Query(channelEntryCSVName)
			if err != nil {
				return err
			}

			if rows.Next() {
				var replaced sql.NullString
				if err := rows.Scan(&replaced); err != nil {
					return err
				}

				if !replaced.Valid || replaced.String == "" {
					break
				}

				replacedChannelEntry, err := addChannelEntry.Exec(c.Name, manifest.PackageName, replaced.String, depth)
				if err != nil {
					return err
				}
				replacedID, err := replacedChannelEntry.LastInsertId()
				if err != nil {
					return err
				}
				addReplaces.Exec(replacedID, currentID)
				currentID = replacedID
				channelEntryCSVName = replaced.String
				depth += 1
			} else {
				return fmt.Errorf("%s specifies replacement that couldn't be found", c.CurrentCSVName)
			}
		}
	}
	return tx.Commit()
}

func (s *SQLLoader) AddProvidedAPIs() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	addAPI, err := tx.Prepare("insert or replace into api(group_name, version, kind, plural) values(?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addAPI.Close()

	addAPIProvider, err := tx.Prepare("insert into api_provider(group_name, version, kind, channel_entry_id) values(?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addAPIProvider.Close()

	// get CRD provided APIs
	getChannelEntryProvidedAPIs, err := tx.Prepare(`
	SELECT DISTINCT channel_entry.entry_id, json_extract(json_each.value, '$.name', '$.version', '$.kind')
	FROM channel_entry INNER JOIN operatorbundle,json_each(operatorbundle.csv, '$.spec.customresourcedefinitions.owned')
	ON channel_entry.operatorbundle_name = operatorbundle.name`)
	if err != nil {
		return err
	}
	defer getChannelEntryProvidedAPIs.Close()

	rows, err := getChannelEntryProvidedAPIs.Query()
	if err != nil {
		return err
	}
	for rows.Next() {
		var channelId sql.NullInt64
		var gvkSQL sql.NullString

		if err := rows.Scan(&channelId, &gvkSQL); err != nil {
			return err
		}
		apigvk := []string{}
		if err := json.Unmarshal([]byte(gvkSQL.String), &apigvk); err != nil {
			return err
		}
		plural, group, err := SplitCRDName(apigvk[0])
		if err != nil {
			return err
		}
		if _, err := addAPI.Exec(group, apigvk[1], apigvk[2], plural); err != nil {
			return err
		}
		if _, err := addAPIProvider.Exec(group, apigvk[1], apigvk[2], channelId.Int64); err != nil {
			return err
		}
	}

	getChannelEntryProvidedAPIsAPIService, err := tx.Prepare(`
	SELECT DISTINCT channel_entry.entry_id, json_extract(json_each.value, '$.group', '$.version', '$.kind', '$.name')
	FROM channel_entry INNER JOIN operatorbundle,json_each(operatorbundle.csv, '$.spec.apiservicedefinitions.owned')
	ON channel_entry.operatorbundle_name = operatorbundle.name`)
	if err != nil {
		return err
	}
	defer getChannelEntryProvidedAPIsAPIService.Close()

	rows, err = getChannelEntryProvidedAPIsAPIService.Query()
	if err != nil {
		return err
	}
	for rows.Next() {
		var channelId sql.NullInt64
		var gvkSQL sql.NullString

		if err := rows.Scan(&channelId, &gvkSQL); err != nil {
			return err
		}
		apigvk := []string{}
		if err := json.Unmarshal([]byte(gvkSQL.String), &apigvk); err != nil {
			return err
		}
		if _, err := addAPI.Exec(apigvk[0], apigvk[1], apigvk[2], apigvk[3]); err != nil {
			return err
		}
		if _, err := addAPIProvider.Exec(apigvk[0], apigvk[1], apigvk[2], channelId.Int64); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *SQLLoader) Close() {
	s.db.Close()
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
