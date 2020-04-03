package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

type SQLLoader struct {
	db       *sql.DB
	migrator Migrator
}

var _ registry.Load = &SQLLoader{}

func NewSQLLiteLoader(db *sql.DB, opts ...DbOption) (*SQLLoader, error) {
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

	return &SQLLoader{db: db, migrator: migrator}, nil
}

func (s *SQLLoader) Migrate(ctx context.Context) error {
	if s.migrator == nil {
		return fmt.Errorf("no migrator configured")
	}
	return s.migrator.Migrate(ctx)
}

func (s *SQLLoader) AddOperatorBundle(bundle *registry.Bundle) error {
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

func (s *SQLLoader) addOperatorBundle(tx *sql.Tx, bundle *registry.Bundle) error {
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

	return s.addAPIs(tx, bundle)
}

func (s *SQLLoader) AddPackageChannels(manifest registry.PackageManifest) error {
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

func (s *SQLLoader) addPackageChannels(tx *sql.Tx, manifest registry.PackageManifest) error {
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
			replaces, skips, err := s.getBundleSkipsReplaces(tx, channelEntryCSVName)
			if err != nil {
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
			if _, _, err := s.getBundleSkipsReplaces(tx, replaces); err != nil {
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

func (s *SQLLoader) ClearNonDefaultBundles(packageName string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
	}()

	// First find the default channel for the package
	getDefChan, err := tx.Prepare(fmt.Sprintf("select default_channel from package where name='%s'", packageName))
	if err != nil {
		return err
	}
	defer getDefChan.Close()

	defaultChannelRows, err := getDefChan.Query()
	if err != nil {
		return err
	}
	defer defaultChannelRows.Close()

	if !defaultChannelRows.Next() {
		return fmt.Errorf("no default channel found for package %s", packageName)
	}
	var defaultChannel sql.NullString
	if err := defaultChannelRows.Scan(&defaultChannel); err != nil {
		return err
	}

	// Then get the head of the default channel
	getChanHead, err := tx.Prepare(fmt.Sprintf("select head_operatorbundle_name from channel where name='%s'", defaultChannel.String))
	if err != nil {
		return err
	}
	defer getChanHead.Close()

	chanHeadRows, err := getChanHead.Query()
	if err != nil {
		return err
	}
	defer chanHeadRows.Close()

	if !chanHeadRows.Next() {
		return fmt.Errorf("no channel head found for default channel %s", defaultChannel.String)
	}
	var defChanHead sql.NullString
	if err := chanHeadRows.Scan(&defChanHead); err != nil {
		return err
	}

	// Now get all the bundles that are not the head of the default channel
	getChannelBundles, err := tx.Prepare(fmt.Sprintf("SELECT operatorbundle_name FROM channel_entry WHERE package_name='%s' AND operatorbundle_name!='%s'", packageName, defChanHead.String))
	if err != nil {
		return err
	}
	defer getChanHead.Close()

	chanBundleRows, err := getChannelBundles.Query()
	if err != nil {
		return err
	}
	defer chanBundleRows.Close()

	bundles := make(map[string]struct{}, 0)
	for chanBundleRows.Next() {
		var bundleToUpdate sql.NullString
		if err := chanBundleRows.Scan(&bundleToUpdate); err != nil {
			return err
		}
		bundles[bundleToUpdate.String] = struct{}{}
	}

	if len(bundles) > 0 {
		bundlePredicates := []string{}
		for bundle := range bundles {
			bundlePredicates = append(bundlePredicates, fmt.Sprintf("name = '%s'", bundle))
		}

		var transactionPredicate string
		if len(bundlePredicates) == 1 {
			transactionPredicate = fmt.Sprintf("WHERE %s AND bundlepath != \"\"", bundlePredicates[0])
		} else {
			transactionPredicate = fmt.Sprintf("WHERE (%s) AND bundlepath != \"\"", strings.Join(bundlePredicates, " OR "))
		}

		removeOldBundles, err := tx.Prepare(fmt.Sprintf("UPDATE operatorbundle SET bundle = null, csv = null %s", transactionPredicate))
		if err != nil {
			return err
		}

		_, err = removeOldBundles.Exec()
		if err != nil {
			return fmt.Errorf("Unable to remove previous bundles: %s", err)
		}
	}

	return tx.Commit()
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

func (s *SQLLoader) getBundleSkipsReplaces(tx *sql.Tx, bundleName string) (replaces string, skips []string, err error) {
	getReplacesAndSkips, err := tx.Prepare(`
	  SELECT replaces, skips 
	  FROM operatorbundle
	  WHERE operatorbundle.name=? LIMIT 1`)
	if err != nil {
		return
	}
	defer getReplacesAndSkips.Close()

	rows, rerr := getReplacesAndSkips.Query(bundleName)
	if err != nil {
		err = rerr
		return
	}
	if !rows.Next() {
		err = fmt.Errorf("no bundle found for bundlename %s", bundleName)
		return
	}

	var replacesStringSQL sql.NullString
	var skipsStringSql sql.NullString
	if err = rows.Scan(&replacesStringSQL, &skipsStringSql); err != nil {
		return
	}

	if replacesStringSQL.Valid {
		replaces = replacesStringSQL.String
	}
	if skipsStringSql.Valid && len(skipsStringSql.String) > 0 {
		skips = strings.Split(skipsStringSql.String, ",")
	}

	return
}

func (s *SQLLoader) addAPIs(tx *sql.Tx, bundle *registry.Bundle) error {
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

func (s *SQLLoader) getCSVNames(tx *sql.Tx, packageName string) ([]string, error) {
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

func (s *SQLLoader) RemovePackage(packageName string) error {
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

func (s *SQLLoader) rmBundle(tx *sql.Tx, csvName string) error {
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

func (s *SQLLoader) AddBundlePackageChannels(manifest registry.PackageManifest, bundle *registry.Bundle) error {
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
