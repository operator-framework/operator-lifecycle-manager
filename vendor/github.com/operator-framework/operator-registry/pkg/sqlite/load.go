package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

type sqlLoader struct {
	db       *sql.DB
	migrator Migrator
}

type MigratableLoader interface {
	registry.Load
	Migrate(context.Context) error
}

var _ MigratableLoader = &sqlLoader{}

func NewSQLLiteLoader(db *sql.DB, opts ...DbOption) (MigratableLoader, error) {
	options := defaultDBOptions()
	for _, o := range opts {
		o(options)
	}

	if _, err := db.Exec("PRAGMA foreign_keys = ON", nil); err != nil {
		return nil, err
	}

	migrator, err := options.MigratorBuilder(db)
	if err != nil {
		return nil, err
	}

	return &sqlLoader{db: db, migrator: migrator}, nil
}

func (s *sqlLoader) Migrate(ctx context.Context) error {
	if s.migrator == nil {
		return fmt.Errorf("no migrator configured")
	}
	return s.migrator.Migrate(ctx)
}

func (s *sqlLoader) AddOperatorBundle(bundle *registry.Bundle) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
	}()

	if err := s.addOperatorBundle(tx, bundle); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *sqlLoader) addOperatorBundle(tx *sql.Tx, bundle *registry.Bundle) error {
	addBundle, err := tx.Prepare("insert into operatorbundle(name, csv, bundle, bundlepath, version, skiprange, replaces, skips) values(?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addBundle.Close()

	addImage, err := tx.Prepare("insert into related_image(image, operatorbundle_name) values(?,?)")
	if err != nil {
		return err
	}
	defer addImage.Close()

	csvName, bundleImage, csvBytes, bundleBytes, err := bundle.Serialize()
	if err != nil {
		return err
	}

	if csvName == "" {
		return fmt.Errorf("csv name not found")
	}

	version, err := bundle.Version()
	if err != nil {
		return err
	}
	skiprange, err := bundle.SkipRange()
	if err != nil {
		return err
	}
	replaces, err := bundle.Replaces()
	if err != nil {
		return err
	}
	skips, err := bundle.Skips()
	if err != nil {
		return err
	}

	if _, err := addBundle.Exec(csvName, csvBytes, bundleBytes, bundleImage, version, skiprange, replaces, strings.Join(skips, ",")); err != nil {
		return err
	}

	imgs, err := bundle.Images()
	if err != nil {
		return err
	}
	for img := range imgs {
		if _, err := addImage.Exec(img, csvName); err != nil {
			return err
		}
	}

	// Add dependencies information
	err = s.addDependencies(tx, bundle)
	if err != nil {
		return err
	}

	err = s.addBundleProperties(tx, bundle)
	if err != nil {
		return err
	}

	return s.addAPIs(tx, bundle)
}

func (s *sqlLoader) AddPackageChannelsFromGraph(graph *registry.Package) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
	}()

	var errs []error

	if err := addPackageIfNotExists(tx, graph.Name); err != nil {
		errs = append(errs, err)
	}

	for name, channel := range graph.Channels {
		if err := addOrUpdateChannel(tx, name, graph.Name, channel.Head.CsvName); err != nil {
			errs = append(errs, err)
			continue
		}
	}

	if err := updateDefaultChannel(tx, graph.DefaultChannel, graph.Name); err != nil {
		errs = append(errs, err)
	}

	// update each channel's graph
	for channelName, channel := range graph.Channels {
		currentNode := channel.Head
		depth := 1

		var previousNodeID int64

		// first clear the current channel graph
		err := truncChannelGraph(tx, channelName, graph.Name)
		if err != nil {
			errs = append(errs, err)
			break
		}

		// iterate into the replacement chain of the channel to insert or update all entries
		for {
			// create real channel entry for node
			id, err := addChannelEntry(tx, channelName, graph.Name, currentNode.CsvName, depth)
			if err != nil {
				errs = append(errs, err)
				break
			}

			// If the previous node was created, use the entryId of the current node to update
			// the replaces for the previous node
			if previousNodeID != 0 {
				err := addReplaces(tx, id, previousNodeID)
				if err != nil {
					errs = append(errs, err)
				}
			}

			syntheticReplaces := make([]registry.BundleKey, 0) // for CSV skips
			nextNode := registry.BundleKey{}

			currentNodeReplaces := channel.Nodes[currentNode]

			// Iterate over all replaces for the node in the graph
			// It should only contain one real replacement, so let's find it and
			// follow the chain. For the rest, they are fake entries and should be
			// generated as synthetic replacements
			for replace := range currentNodeReplaces {
				if _, ok := channel.Nodes[replace]; !ok {
					syntheticReplaces = append(syntheticReplaces, replace)
				} else {
					nextNode = replace
				}
			}

			// create synthetic channel entries for nodes
			// also create channel entry to replace that node
			syntheticDepth := depth + 1
			for _, synthetic := range syntheticReplaces {
				syntheticReplacesID, err := addChannelEntry(tx, channelName, graph.Name, synthetic.CsvName, syntheticDepth)
				if err != nil {
					errs = append(errs, err)
					break
				}

				syntheticNodeID, err := addChannelEntry(tx, channelName, graph.Name, currentNode.CsvName, syntheticDepth)
				if err != nil {
					errs = append(errs, err)
					break
				}

				err = addReplaces(tx, syntheticReplacesID, syntheticNodeID)
				if err != nil {
					errs = append(errs, err)
				}
				syntheticDepth++
			}

			// we got to the end of the channel graph
			if nextNode.IsEmpty() {
				if len(channel.Nodes) != depth {
					err := fmt.Errorf("Invalid graph: some (non-bottom) nodes defined in the graph were not mentioned as replacements of any node")
					errs = append(errs, err)
				}
				break
			}

			// increase depth and continue
			currentNode = nextNode
			previousNodeID = id
			depth++
		}
	}

	if err := tx.Commit(); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

func (s *sqlLoader) AddPackageChannels(manifest registry.PackageManifest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
	}()

	if err := s.addPackageChannels(tx, manifest); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *sqlLoader) addPackageChannels(tx *sql.Tx, manifest registry.PackageManifest) error {
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

	getReplaces, err := tx.Prepare(`
	  SELECT DISTINCT operatorbundle.csv
	  FROM operatorbundle
	  WHERE operatorbundle.name=? LIMIT 1`)
	if err != nil {
		return err
	}
	defer getReplaces.Close()

	var errs []error

	if _, err := addPackage.Exec(manifest.PackageName); err != nil {
		errs = append(errs, err)
		return utilerrors.NewAggregate(errs)
	}

	hasDefault := false
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

		// Since this loop depends on following 'replaces', keep track of where it's been
		replaceCycle := map[string]bool{channelEntryCSVName: true}
		for {
			// Get CSV for current entry
			replaces, skips, version, err := s.getBundleSkipsReplacesVersion(tx, channelEntryCSVName)
			if err != nil {
				errs = append(errs, err)
				break
			}

			if err := s.addPackageProperty(tx, channelEntryCSVName, manifest.PackageName, version); err != nil {
				errs = append(errs, err)
				break
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

				depth++
			}

			// create real replacement chain
			if replaces == "" {
				// we've walked the channel until there was no replacement
				break
			}

			replacedChannelEntry, err := addChannelEntry.Exec(c.Name, manifest.PackageName, replaces, depth)
			if err != nil {
				errs = append(errs, err)
				break
			}

			// If we find 'replaces' in the circuit list then we've seen it already, break out
			if _, ok := replaceCycle[replaces]; ok {
				errs = append(errs, fmt.Errorf("Cycle detected, %s replaces %s", channelEntryCSVName, replaces))
				break
			}
			replaceCycle[replaces] = true

			replacedID, err := replacedChannelEntry.LastInsertId()
			if err != nil {
				errs = append(errs, err)
				break
			}
			if _, err = addReplaces.Exec(replacedID, currentID); err != nil {
				errs = append(errs, err)
				break
			}
			if _, _, _, err := s.getBundleSkipsReplacesVersion(tx, replaces); err != nil {
				errs = append(errs, fmt.Errorf("%s specifies replacement that couldn't be found", c.CurrentCSVName))
				break
			}

			currentID = replacedID
			channelEntryCSVName = replaces
			depth++
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (s *sqlLoader) ClearNonHeadBundles() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
	}()

	removeNonHeadBundles, err := tx.Prepare(`
		update operatorbundle set bundle = null, csv = null
		where (bundlepath != null or bundlepath != "")
		and name not in (
			select operatorbundle.name from operatorbundle
			join channel on channel.head_operatorbundle_name = operatorbundle.name
		)
	`)
	if err != nil {
		return err
	}
	defer removeNonHeadBundles.Close()

	_, err = removeNonHeadBundles.Exec()
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqlLoader) getBundleSkipsReplacesVersion(tx *sql.Tx, bundleName string) (replaces string, skips []string, version string, err error) {
	getReplacesSkipsAndVersions, err := tx.Prepare(`
	  SELECT replaces, skips, version
	  FROM operatorbundle
	  WHERE operatorbundle.name=? LIMIT 1`)
	if err != nil {
		return
	}
	defer getReplacesSkipsAndVersions.Close()

	rows, rerr := getReplacesSkipsAndVersions.Query(bundleName)
	if err != nil {
		err = rerr
		return
	}
	if !rows.Next() {
		err = fmt.Errorf("no bundle found for bundlename %s", bundleName)
		return
	}

	var replacesStringSQL sql.NullString
	var skipsStringSQL sql.NullString
	var versionStringSQL sql.NullString
	if err = rows.Scan(&replacesStringSQL, &skipsStringSQL, &versionStringSQL); err != nil {
		return
	}

	if replacesStringSQL.Valid {
		replaces = replacesStringSQL.String
	}
	if skipsStringSQL.Valid && len(skipsStringSQL.String) > 0 {
		skips = strings.Split(skipsStringSQL.String, ",")
	}
	if versionStringSQL.Valid {
		version = versionStringSQL.String
	}

	return
}

func (s *sqlLoader) addAPIs(tx *sql.Tx, bundle *registry.Bundle) error {
	if bundle.Name == "" {
		return fmt.Errorf("cannot add apis for bundle with no name: %#v", bundle)
	}
	addAPI, err := tx.Prepare("insert or ignore into api(group_name, version, kind, plural) values(?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addAPI.Close()

	addAPIProvider, err := tx.Prepare("insert into api_provider(group_name, version, kind, operatorbundle_name, operatorbundle_version, operatorbundle_path) values(?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addAPIProvider.Close()

	addAPIRequirer, err := tx.Prepare("insert into api_requirer(group_name, version, kind, operatorbundle_name, operatorbundle_version, operatorbundle_path) values(?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addAPIRequirer.Close()

	providedApis, err := bundle.ProvidedAPIs()
	if err != nil {
		return err
	}
	requiredApis, err := bundle.RequiredAPIs()
	if err != nil {
		return err
	}
	bundleVersion, err := bundle.Version()
	if err != nil {
		return err
	}

	sqlString := func(s string) sql.NullString {
		return sql.NullString{String: s, Valid: s != ""}
	}
	for api := range providedApis {
		if _, err := addAPI.Exec(api.Group, api.Version, api.Kind, api.Plural); err != nil {
			return err
		}

		if _, err := addAPIProvider.Exec(api.Group, api.Version, api.Kind, bundle.Name, sqlString(bundleVersion), sqlString(bundle.BundleImage)); err != nil {
			return err
		}
	}
	for api := range requiredApis {
		if _, err := addAPI.Exec(api.Group, api.Version, api.Kind, api.Plural); err != nil {
			return err
		}

		if _, err := addAPIRequirer.Exec(api.Group, api.Version, api.Kind, bundle.Name, sqlString(bundleVersion), sqlString(bundle.BundleImage)); err != nil {
			return err
		}
	}

	return nil
}

func (s *sqlLoader) getCSVNames(tx *sql.Tx, packageName string) ([]string, error) {
	getID, err := tx.Prepare(`
	  SELECT DISTINCT channel_entry.operatorbundle_name
	  FROM channel_entry
	  WHERE channel_entry.package_name=?`)

	if err != nil {
		return nil, err
	}
	defer getID.Close()

	rows, err := getID.Query(packageName)
	if err != nil {
		return nil, err
	}

	var csvName string
	csvNames := []string{}
	for rows.Next() {
		err := rows.Scan(&csvName)
		if err != nil {
			return nil, err
		}
		csvNames = append(csvNames, csvName)
	}

	if err := rows.Close(); err != nil {
		return nil, err
	}

	return csvNames, nil
}

func (s *sqlLoader) RemovePackage(packageName string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
	}()

	csvNames, err := s.getCSVNames(tx, packageName)
	if err != nil {
		return err
	}
	for _, csvName := range csvNames {
		err = s.rmBundle(tx, csvName)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *sqlLoader) rmBundle(tx *sql.Tx, csvName string) error {
	stmt, err := tx.Prepare("DELETE FROM operatorbundle WHERE operatorbundle.name=?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	if _, err := stmt.Exec(csvName); err != nil {
		return err
	}

	return nil
}

func (s *sqlLoader) AddBundleSemver(graph *registry.Package, bundle *registry.Bundle) error {
	err := s.AddOperatorBundle(bundle)
	if err != nil {
		return err
	}

	err = s.AddPackageChannelsFromGraph(graph)
	if err != nil {
		return err
	}

	return nil
}

func (s *sqlLoader) AddBundlePackageChannels(manifest registry.PackageManifest, bundle *registry.Bundle) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
	}()

	if err := s.addOperatorBundle(tx, bundle); err != nil {
		return err
	}

	// Delete package and channels (entries will cascade) - they will be recalculated
	deletePkg, err := tx.Prepare("delete from package where name = ?")
	if err != nil {
		return err
	}
	defer deletePkg.Close()
	_, err = deletePkg.Exec(manifest.PackageName)
	if err != nil {
		return err
	}
	deleteChan, err := tx.Prepare("delete from channel where package_name = ?")
	if err != nil {
		return err
	}
	defer deleteChan.Close()
	_, err = deleteChan.Exec(manifest.PackageName)
	if err != nil {
		return err
	}

	if err := s.addPackageChannels(tx, manifest); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *sqlLoader) addDependencies(tx *sql.Tx, bundle *registry.Bundle) error {
	addDep, err := tx.Prepare("insert into dependencies(type, value, operatorbundle_name, operatorbundle_version, operatorbundle_path) values(?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addDep.Close()

	bundleVersion, err := bundle.Version()
	if err != nil {
		return err
	}

	sqlString := func(s string) sql.NullString {
		return sql.NullString{String: s, Valid: s != ""}
	}
	for _, dep := range bundle.Dependencies {
		if _, err := addDep.Exec(dep.Type, dep.Value, bundle.Name, sqlString(bundleVersion), sqlString(bundle.BundleImage)); err != nil {
			return err
		}
	}

	// Look up requiredAPIs in CSV and add them in dependencies table
	requiredApis, err := bundle.RequiredAPIs()
	if err != nil {
		return err
	}

	for api := range requiredApis {
		dep := registry.GVKDependency{
			Group:   api.Group,
			Kind:    api.Kind,
			Version: api.Version,
		}
		value, err := json.Marshal(dep)
		if err != nil {
			return err
		}
		if _, err := addDep.Exec(registry.GVKType, value, bundle.Name, sqlString(bundleVersion), sqlString(bundle.BundleImage)); err != nil {
			return err
		}
	}

	return nil
}

func (s *sqlLoader) addProperty(tx *sql.Tx, propType, value, bundleName, version, path string) error {
	addProp, err := tx.Prepare("insert into properties(type, value, operatorbundle_name, operatorbundle_version, operatorbundle_path) values(?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addProp.Close()

	sqlString := func(s string) sql.NullString {
		return sql.NullString{String: s, Valid: s != ""}
	}

	if _, err := addProp.Exec(propType, value, bundleName, sqlString(version), sqlString(path)); err != nil {
		return err
	}
	return nil
}

func (s *sqlLoader) addPackageProperty(tx *sql.Tx, bundleName, pkg, version string) error {
	// Add the package property
	prop := registry.PackageProperty{
		PackageName: pkg,
		Version:     version,
	}
	value, err := json.Marshal(prop)
	if err != nil {
		return err
	}

	return s.addProperty(tx, registry.PackageType, string(value), bundleName, version, "")
}

func (s *sqlLoader) addBundleProperties(tx *sql.Tx, bundle *registry.Bundle) error {
	bundleVersion, err := bundle.Version()
	if err != nil {
		return err
	}

	for _, prop := range bundle.Properties {
		value, _ := json.Marshal(prop.Value)
		if err := s.addProperty(tx, prop.Type, string(value), bundle.Name, bundleVersion, bundle.BundleImage); err != nil {
			return err
		}
	}

	// Look up providedAPIs in CSV and add them in properties table
	providedApis, err := bundle.ProvidedAPIs()
	if err != nil {
		return err
	}

	for api := range providedApis {
		prop := registry.GVKProperty{
			Group:   api.Group,
			Kind:    api.Kind,
			Version: api.Version,
		}
		value, err := json.Marshal(prop)
		if err != nil {
			return err
		}
		if err := s.addProperty(tx, registry.GVKType, string(value), bundle.Name, bundleVersion, bundle.BundleImage); err != nil {
			return err
		}
	}

	// Add label properties
	if csv, err := bundle.ClusterServiceVersion(); err == nil {
		annotations := csv.ObjectMeta.GetAnnotations()
		if v, ok := annotations[registry.PropertyKey]; ok {
			var props []registry.Property
			if err := json.Unmarshal([]byte(v), &props); err == nil {
				for _, prop := range props {
					// Only add label type from the list
					// TODO: Support more types such as GVK and package
					if prop.Type == registry.LabelType {
						var label registry.LabelProperty
						err := json.Unmarshal(prop.Value, &label)
						if err != nil {
							continue
						}
						value, err := json.Marshal(label)
						if err != nil {
							continue
						}
						if err := s.addProperty(tx, registry.LabelType, string(value), bundle.Name, bundleVersion, bundle.BundleImage); err != nil {
							continue
						}
					}
				}
			}
		}
	}

	return nil
}

func (s *sqlLoader) rmChannelEntry(tx *sql.Tx, csvName string) error {
	getEntryID := `SELECT entry_id FROM channel_entry WHERE operatorbundle_name=?`
	rows, err := tx.QueryContext(context.TODO(), getEntryID, csvName)
	if err != nil {
		return err
	}
	var entryIDs []int64
	for rows.Next() {
		var entryID sql.NullInt64
		rows.Scan(&entryID)
		entryIDs = append(entryIDs, entryID.Int64)
	}
	err = rows.Close()
	if err != nil {
		return err
	}

	updateChannelEntry, err := tx.Prepare(`UPDATE channel_entry SET replaces=NULL WHERE replaces=?`)
	if err != nil {
		return err
	}
	for _, id := range entryIDs {
		if _, err := updateChannelEntry.Exec(id); err != nil {
			return err
		}
	}
	err = updateChannelEntry.Close()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("DELETE FROM channel_entry WHERE operatorbundle_name=?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	if _, err := stmt.Exec(csvName); err != nil {
		return err
	}

	return nil
}

func getTailFromBundle(tx *sql.Tx, name string) (bundles []string, err error) {
	getReplacesSkips := `SELECT replaces, skips FROM operatorbundle WHERE name=?`
	isDefaultChannelHead := `SELECT head_operatorbundle_name FROM channel 
							INNER JOIN package ON channel.name = package.default_channel 
							WHERE channel.head_operatorbundle_name = ?`

	tail := make(map[string]struct{})
	next := name

	for next != "" {
		rows, err := tx.QueryContext(context.TODO(), getReplacesSkips, next)
		if err != nil {
			return nil, err
		}
		var replaces sql.NullString
		var skips sql.NullString
		if rows.Next() {
			if err := rows.Scan(&replaces, &skips); err != nil {
				return nil, err
			}
		}
		rows.Close()
		if skips.Valid && skips.String != "" {
			for _, skip := range strings.Split(skips.String, ",") {
				tail[skip] = struct{}{}
			}
		}
		if replaces.Valid && replaces.String != "" {
			// check if replaces is the head of the defaultChannel
			// if it is, the defaultChannel will be removed
			// this is not allowed because we cannot know which channel to promote as the new default
			rows, err := tx.QueryContext(context.TODO(), isDefaultChannelHead, replaces.String)
			if err != nil {
				return nil, err
			}
			if rows.Next() {
				var defaultChannelHead sql.NullString
				err := rows.Scan(&defaultChannelHead)
				if err != nil {
					return nil, err
				}
				if defaultChannelHead.Valid {
					return nil, registry.ErrRemovingDefaultChannelDuringDeprecation
				}
			}
			next = replaces.String
			tail[replaces.String] = struct{}{}
		} else {
			next = ""
		}
	}
	var allTails []string

	for k := range tail {
		allTails = append(allTails, k)
	}

	return allTails, nil

}

func getBundleNameAndVersionForImage(tx *sql.Tx, path string) (string, string, error) {
	query := `SELECT name, version FROM operatorbundle WHERE bundlepath=? LIMIT 1`
	rows, err := tx.QueryContext(context.TODO(), query, path)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()

	var name sql.NullString
	var version sql.NullString
	if rows.Next() {
		if err := rows.Scan(&name, &version); err != nil {
			return "", "", err
		}
	}
	if name.Valid && version.Valid {
		return name.String, version.String, nil
	}
	return "", "", registry.ErrBundleImageNotInDatabase
}

func (s *sqlLoader) DeprecateBundle(path string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
	}()

	name, version, err := getBundleNameAndVersionForImage(tx, path)
	if err != nil {
		return err
	}
	tailBundles, err := getTailFromBundle(tx, name)
	if err != nil {
		return err
	}

	for _, bundle := range tailBundles {
		err := s.rmBundle(tx, bundle)
		if err != nil {
			return err
		}
		err = s.rmChannelEntry(tx, bundle)
		if err != nil {
			return err
		}
	}

	deprecatedValue, err := json.Marshal(registry.DeprecatedProperty{})
	if err != nil {
		return err
	}
	err = s.addProperty(tx, registry.DeprecatedType, string(deprecatedValue), name, version, path)
	if err != nil {
		return err
	}

	return tx.Commit()
}
