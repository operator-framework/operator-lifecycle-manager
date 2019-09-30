package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"

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
	defer func() {
		tx.Rollback()
	}()

	stmt, err := tx.Prepare("insert into operatorbundle(name, csv, bundle) values(?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	csvName, csvBytes, bundleBytes, err := bundle.Serialize()
	if err != nil {
		return err
	}

	if csvName == "" {
		return fmt.Errorf("csv name not found")
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
	defer func() {
		tx.Rollback()
	}()

	addPackage, err := tx.Prepare("insert into package(name) values(?)")
	if err != nil {
		return err
	}
	defer addPackage.Close()

	addDefaultChannel, err := tx.Prepare("update package set default_channel = ? where name = ?")
	if err != nil {
		return err
	}
	defer addDefaultChannel.Close()

	addChannel, err := tx.Prepare("insert into channel(name, package_name, head_operatorbundle_name) values(?, ?, ?)")
	if err != nil {
		return err
	}
	defer addChannel.Close()

	addChannelEntry, err := tx.Prepare("insert into channel_entry(channel_name, package_name, operatorbundle_name, depth) values(?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addChannelEntry.Close()

	addReplaces, err := tx.Prepare("update channel_entry set replaces = ? where entry_id = ?")
	if err != nil {
		return err
	}
	defer addReplaces.Close()

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

	if _, err := addPackage.Exec(manifest.PackageName); err != nil {
		// This should be terminal
		return err
	}

	hasDefault := false
	var errs []error
	for _, c := range manifest.Channels {
		if _, err := addChannel.Exec(c.Name, manifest.PackageName, c.CurrentCSVName); err != nil {
			errs = append(errs, err)
			continue
		}
		if c.IsDefaultChannel(manifest) {
			hasDefault = true
			if _, err := addDefaultChannel.Exec(c.Name, manifest.PackageName); err != nil {
				errs = append(errs, err)
				continue
			}
		}
	}
	if !hasDefault {
		errs = append(errs, fmt.Errorf("no default channel specified for %s", manifest.PackageName))
	}

	for _, c := range manifest.Channels {
		res, err := addChannelEntry.Exec(c.Name, manifest.PackageName, c.CurrentCSVName, 0)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		currentID, err := res.LastInsertId()
		if err != nil {
			errs = append(errs, err)
			continue
		}

		channelEntryCSVName := c.CurrentCSVName
		depth := 1
		for {

			// Get CSV for current entry
			channelEntryCSV, err := s.getCSV(tx, channelEntryCSVName)
			if err != nil {
				errs = append(errs, err)
				break
			}

			if err := s.addProvidedAPIs(tx, channelEntryCSV, currentID); err != nil {
				errs = append(errs, err)
			}

			skips, err := channelEntryCSV.GetSkips()
			if err != nil {
				errs = append(errs, err)
			}

			for _, skip := range skips {
				// add dummy channel entry for the skipped version
				skippedChannelEntry, err := addChannelEntry.Exec(c.Name, manifest.PackageName, skip, depth)
				if err != nil {
					errs = append(errs, err)
					continue
				}

				skippedID, err := skippedChannelEntry.LastInsertId()
				if err != nil {
					errs = append(errs, err)
					continue
				}

				// add another channel entry for the parent, which replaces the skipped
				synthesizedChannelEntry, err := addChannelEntry.Exec(c.Name, manifest.PackageName, channelEntryCSVName, depth)
				if err != nil {
					errs = append(errs, err)
					continue
				}

				synthesizedID, err := synthesizedChannelEntry.LastInsertId()
				if err != nil {
					errs = append(errs, err)
					continue
				}

				if _, err = addReplaces.Exec(skippedID, synthesizedID); err != nil {
					errs = append(errs, err)
					continue
				}

				if err := s.addProvidedAPIs(tx, channelEntryCSV, synthesizedID); err != nil {
					errs = append(errs, err)
					continue
				}

				depth++
			}

			// create real replacement chain
			replaces, err := channelEntryCSV.GetReplaces()
			if err != nil {
				errs = append(errs, err)
				break
			}

			if replaces == "" {
				// we've walked the channel until there was no replacement
				break
			}

			replacedChannelEntry, err := addChannelEntry.Exec(c.Name, manifest.PackageName, replaces, depth)
			if err != nil {
				errs = append(errs, err)
				break
			}
			replacedID, err := replacedChannelEntry.LastInsertId()
			if err != nil {
				errs = append(errs, err)
				break
			}
			if _, err = addReplaces.Exec(replacedID, currentID); err != nil {
				errs = append(errs, err)
				break
			}
			if _, err := s.getCSV(tx, replaces); err != nil {
				errs = append(errs, fmt.Errorf("%s specifies replacement that couldn't be found", c.CurrentCSVName))
				break
			}

			currentID = replacedID
			channelEntryCSVName = replaces
			depth++
		}
	}

	if err := tx.Commit(); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

func (s *SQLLoader) Close() error {
	return s.db.Close()
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

func (s *SQLLoader) getCSV(tx *sql.Tx, csvName string) (*registry.ClusterServiceVersion, error) {
	getCSV, err := tx.Prepare(`
	  SELECT DISTINCT operatorbundle.csv 
	  FROM operatorbundle
	  WHERE operatorbundle.name=? LIMIT 1`)
	if err != nil {
		return nil, err
	}
	defer getCSV.Close()

	rows, err := getCSV.Query(csvName)
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		return nil, fmt.Errorf("no bundle found for csv %s", csvName)
	}
	var csvStringSQL sql.NullString
	if err := rows.Scan(&csvStringSQL); err != nil {
		return nil, err
	}

	dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(csvStringSQL.String), 10)
	unst := &unstructured.Unstructured{}
	if err := dec.Decode(unst); err != nil {
		return nil, err
	}

	csv := &registry.ClusterServiceVersion{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unst.UnstructuredContent(), csv); err != nil {
		return nil, err
	}

	return csv, nil
}

func (s *SQLLoader) addProvidedAPIs(tx *sql.Tx, csv *registry.ClusterServiceVersion, channelEntryId int64) error {
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

	ownedCRDs, _, err := csv.GetCustomResourceDefintions()
	for _, crd := range ownedCRDs {
		plural, group, err := SplitCRDName(crd.Name)
		if err != nil {
			return err
		}
		if _, err := addAPI.Exec(group, crd.Version, crd.Kind, plural); err != nil {
			return err
		}
		if _, err := addAPIProvider.Exec(group, crd.Version, crd.Kind, channelEntryId); err != nil {
			return err
		}
	}

	ownedAPIs, _, err := csv.GetApiServiceDefinitions()
	for _, api := range ownedAPIs {
		if _, err := addAPI.Exec(api.Group, api.Version, api.Kind, api.Name); err != nil {
			return err
		}
		if _, err := addAPIProvider.Exec(api.Group, api.Version, api.Kind, channelEntryId); err != nil {
			return err
		}
	}
	return nil
}
