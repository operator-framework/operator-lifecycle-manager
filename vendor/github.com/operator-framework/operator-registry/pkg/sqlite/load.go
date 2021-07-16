package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blang/semver"
	_ "github.com/mattn/go-sqlite3"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	libsemver "github.com/operator-framework/operator-registry/pkg/lib/semver"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

type sqlLoader struct {
	db          *sql.DB
	migrator    Migrator
	enableAlpha bool
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

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, err
	}

	migrator, err := options.MigratorBuilder(db)
	if err != nil {
		return nil, err
	}

	return &sqlLoader{db: db, migrator: migrator, enableAlpha: options.EnableAlpha}, nil
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
	addBundle, err := tx.Prepare("insert into operatorbundle(name, csv, bundle, bundlepath, version, skiprange, replaces, skips, substitutesfor) values(?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer addBundle.Close()

	addImage, err := tx.Prepare("insert into related_image(image, operatorbundle_name) values(?,?)")
	if err != nil {
		return err
	}
	defer addImage.Close()

	csvName, bundleImage, csvBytes, bundleBytes, _, err := bundle.Serialize()
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
	substitutesFor, err := bundle.SubstitutesFor()
	if err != nil {
		return err
	}

	if substitutesFor != "" && !s.enableAlpha {
		return fmt.Errorf("SubstitutesFor is an alpha-only feature. You must enable alpha features with the flag --enable-alpha in order to use this feature.")
	}

	if _, err := addBundle.Exec(csvName, csvBytes, bundleBytes, bundleImage, version, skiprange, replaces, strings.Join(skips, ","), substitutesFor); err != nil {
		return fmt.Errorf("failed to add bundle %q: %s", csvName, err.Error())
	}

	imgs, err := bundle.Images()
	if err != nil {
		return err
	}
	for img := range imgs {
		if _, err := addImage.Exec(img, csvName); err != nil {
			return fmt.Errorf("failed to add related images %q for bundle %q: %s", img, csvName, err.Error())
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

	if s.enableAlpha {
		err = s.addSubstitutesFor(tx, bundle)
		if err != nil {
			return err
		}
	}

	return s.addAPIs(tx, bundle)
}

func (s *sqlLoader) addSubstitutesFor(tx *sql.Tx, bundle *registry.Bundle) error {

	updateBundleReplaces, err := tx.Prepare("update operatorbundle set replaces = ? where replaces = ?")
	if err != nil {
		return err
	}
	defer updateBundleReplaces.Close()

	updateBundleSkips, err := tx.Prepare("update operatorbundle set skips = ? where name = ?")
	if err != nil {
		return err
	}
	defer updateBundleSkips.Close()

	updateBundleSubstitutesFor, err := tx.Prepare("update operatorbundle set substitutesfor = ? where name = ?")
	if err != nil {
		return err
	}
	defer updateBundleSubstitutesFor.Close()

	updateBundleReplacesSkips, err := tx.Prepare("update operatorbundle set replaces = ?, skips = ? where name = ?")
	if err != nil {
		return err
	}
	defer updateBundleReplacesSkips.Close()

	csvName := bundle.Name

	replaces, err := bundle.Replaces()
	if err != nil {
		return err
	}
	skips, err := bundle.Skips()
	if err != nil {
		return err
	}
	version, err := bundle.Version()
	if err != nil {
		return err
	}
	substitutesFor, err := bundle.SubstitutesFor()
	if err != nil {
		return err
	}
	if substitutesFor != "" {
		// Update any replaces that reference the substituted-for bundle
		_, err = updateBundleReplaces.Exec(csvName, substitutesFor)
		if err != nil {
			return err
		}
		// Check if any other bundle substitutes for the same bundle
		otherSubstitutions, err := s.getBundlesThatSubstitutesFor(tx, substitutesFor)
		if err != nil {
			return err
		}
		for len(otherSubstitutions) > 0 {
			// consume the slice of substitutions
			otherSubstitution := otherSubstitutions[0]
			otherSubstitutions = otherSubstitutions[1:]
			if otherSubstitution != csvName {
				// Another bundle is substituting for that same bundle
				// Get other bundle's version
				_, _, rawVersion, err := s.getBundleSkipsReplacesVersion(tx, otherSubstitution)
				if err != nil {
					return err
				}
				otherSubstitutionVersion, err := semver.Parse(rawVersion)
				if err != nil {
					return err
				}
				currentSubstitutionVersion, err := semver.Parse(version)
				if err != nil {
					return err
				}
				// Compare versions
				c, err := libsemver.BuildIdCompare(otherSubstitutionVersion, currentSubstitutionVersion)
				if err != nil {
					return err
				}
				if c < 0 {
					// Update the currentSubstitution substitutesFor to point to otherSubstitution
					// since it is latest
					_, err = updateBundleSubstitutesFor.Exec(otherSubstitution, csvName)
					if err != nil {
						return err
					}
					moreSubstitutions, err := s.getBundlesThatSubstitutesFor(tx, otherSubstitution)
					if err != nil {
						return err
					}
					otherSubstitutions = append(otherSubstitutions, moreSubstitutions...)
				} else if c > 0 {
					// Update the otherSubstitution's substitutesFor to point to csvName
					// Since it is the latest
					_, err = updateBundleSubstitutesFor.Exec(csvName, otherSubstitution)
					if err != nil {
						return err
					}
					// Update the otherSubstitution's skips to include csvName and its skips
					err = s.appendSkips(tx, append(skips, csvName), otherSubstitution)
					if err != nil {
						return err
					}
					moreSubstitutions, err := s.getBundlesThatSubstitutesFor(tx, csvName)
					if err != nil {
						return err
					}
					if len(moreSubstitutions) > 1 {
						return fmt.Errorf("programmer error: more than one substitution pointing to %s", csvName)
					}
				} else {
					// the versions are equal
					return fmt.Errorf("cannot determine latest substitution because of duplicate versions")
				}
			}
		}
	}

	// Get latest substitutesFor value of the current bundle
	substitutesFor, err = s.getBundleSubstitution(tx, csvName)
	if err != nil {
		return err
	}

	// If the substituted-for of the current bundle substitutes for another bundle
	// it should also be added to the skips of the substitutesFor bundle
	for substitutesFor != "" {
		skips = append(skips, substitutesFor)
		substitutesFor, err = s.getBundleSubstitution(tx, substitutesFor)
		if err != nil {
			return err
		}
	}

	// If the substitution (or substitution of substitution) is added before the
	// substituted for bundle, (i.e. the bundle being added is substituted for by
	// another bundle) then transfer the skips from the substitutedFor bundle (this
	// bundle) over to the substitution's skips
	var substitutesFors []string
	substitutesFors, err = s.getBundlesThatSubstitutesFor(tx, csvName)
	if err != nil || len(substitutesFors) > 1 {
		return err
	}
	for len(substitutesFors) > 0 {
		err = s.appendSkips(tx, append(skips, csvName), substitutesFors[0])
		if err != nil {
			return err
		}
		substitutesFors, err = s.getBundlesThatSubstitutesFor(tx, substitutesFors[0])
		if err != nil || len(substitutesFors) > 1 {
			return err
		}
	}

	// Bundles that skip a bundle that is substituted for
	// should also skip the substituted-for bundle
	if len(skips) != 0 {
		// ensure slice of skips doesn't contain duplicates
		substitutesSkips := make(map[string]struct{})
		skipsOverwrite := []string{}
		for _, skip := range skips {
			substitutesSkips[skip] = struct{}{}
			substitutesFors, err = s.getBundlesThatSubstitutesFor(tx, skip)
			if err != nil || len(substitutesFors) > 1 {
				return err
			}
			for len(substitutesFors) > 0 {
				// consume the slice of substitutions
				substitutesFor = substitutesFors[0]
				substitutesFors = substitutesFors[1:]
				// shouldn't skip yourself
				if substitutesFor != csvName {
					substitutesSkips[substitutesFor] = struct{}{}
					substitutesFors, err = s.getBundlesThatSubstitutesFor(tx, substitutesFor)
					if err != nil || len(substitutesFors) > 1 {
						return err
					}
				}
			}
		}
		for s := range substitutesSkips {
			skipsOverwrite = append(skipsOverwrite, s)
		}
		skips = skipsOverwrite
	}

	// If the bundle being added replaces a bundle that is substituted for
	// (for example it was the previous head of the channel), change
	// the replaces to the substituted-for bundle
	if replaces != "" {
		substitutesFors, err = s.getBundlesThatSubstitutesFor(tx, replaces)
		if err != nil {
			return err
		}
		for len(substitutesFors) > 0 {
			// update the replaces to a newer substitution
			replaces = substitutesFors[0]
			// try to get the substitution of the substitution
			substitutesFors, err = s.getBundlesThatSubstitutesFor(tx, replaces)
			if err != nil || len(substitutesFors) > 1 {
				return err
			}
		}
	}

	_, err = updateBundleReplacesSkips.Exec(replaces, strings.Join(skips, ","), csvName)
	if err != nil {
		return err
	}

	return nil
}

func (s *sqlLoader) appendSkips(tx *sql.Tx, skips []string, csvName string) error {
	updateSkips, err := tx.Prepare("update operatorbundle set skips = ? where name = ?")
	if err != nil {
		return err
	}
	defer updateSkips.Close()

	_, currentSkips, _, err := s.getBundleSkipsReplacesVersion(tx, csvName)
	if err != nil {
		return err
	}

	// ensure slice of skips doesn't contain duplicates
	skipsMap := make(map[string]struct{})
	for _, skip := range currentSkips {
		skipsMap[skip] = struct{}{}
	}
	for _, skip := range skips {
		if _, ok := skipsMap[skip]; !ok {
			currentSkips = append(currentSkips, skip)
		}
	}

	_, err = updateSkips.Exec(strings.Join(currentSkips, ","), csvName)
	return err
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

	if err := s.rmPackage(tx, manifest.PackageName); err != nil {
		return err
	}

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

	if _, err := addPackage.Exec(manifest.PackageName); err != nil {
		return fmt.Errorf("failed to add package %q: %s", manifest.PackageName, err.Error())
	}

	var (
		errs       []error
		channels   []registry.PackageChannel
		hasDefault bool
	)
	for _, c := range manifest.Channels {
		if deprecated, err := s.deprecated(tx, c.CurrentCSVName); err != nil || deprecated {
			// Elide channels that start with a deprecated bundle
			continue
		}
		if _, err := addChannel.Exec(c.Name, manifest.PackageName, c.CurrentCSVName); err != nil {
			errs = append(errs, fmt.Errorf("failed to add channel %q in package %q: %s", c.Name, manifest.PackageName, err.Error()))
			continue
		}
		if c.IsDefaultChannel(manifest) {
			hasDefault = true
			if _, err := addDefaultChannel.Exec(c.Name, manifest.PackageName); err != nil {
				errs = append(errs, fmt.Errorf("failed to add default channel %q in package %q: %s", c.Name, manifest.PackageName, err.Error()))
				continue
			}
		}
		channels = append(channels, c)
	}
	if !hasDefault {
		errs = append(errs, fmt.Errorf("no default channel specified for %s", manifest.PackageName))
	}

	for _, c := range channels {
		res, err := addChannelEntry.Exec(c.Name, manifest.PackageName, c.CurrentCSVName, 0)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to add channel %q in package %q: %s", c.Name, manifest.PackageName, err.Error()))
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

			bundlePath, err := s.getBundlePathIfExists(tx, channelEntryCSVName)
			if err != nil {
				// this should only happen on an SQL error, bundlepath just not being set is for backwards compatibility reasons
				errs = append(errs, err)
				break
			}

			if err := s.addPackageProperty(tx, channelEntryCSVName, manifest.PackageName, version, bundlePath); err != nil {
				errs = append(errs, err)
				break
			}

			for _, skip := range skips {
				// add dummy channel entry for the skipped version
				skippedChannelEntry, err := addChannelEntry.Exec(c.Name, manifest.PackageName, skip, depth)
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to add channel %q for skipped version %q in package %q: %s", c.Name, skip, manifest.PackageName, err.Error()))
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
					errs = append(errs, fmt.Errorf("failed to add channel %q for replaces %q in package %q: %s", c.Name, channelEntryCSVName, manifest.PackageName, err.Error()))
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
				errs = append(errs, fmt.Errorf("failed to add channel %q for replaces %q in package %q: %s", c.Name, replaces, manifest.PackageName, err.Error()))
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
			deprecated, err := s.deprecated(tx, channelEntryCSVName)
			if err != nil {
				errs = append(errs, err)
				break
			}
			if deprecated {
				// The package is truncated below this point, we're done!
				break
			}
			if _, _, _, err := s.getBundleSkipsReplacesVersion(tx, replaces); err != nil {
				errs = append(errs, fmt.Errorf("Invalid bundle %s, replaces nonexistent bundle %s", c.CurrentCSVName, replaces))
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
	defer rows.Close()
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

func (s *sqlLoader) getBundlePathIfExists(tx *sql.Tx, bundleName string) (bundlePath string, err error) {
	getBundlePath, err := tx.Prepare(`
	  SELECT bundlepath
	  FROM operatorbundle
	  WHERE operatorbundle.name=? LIMIT 1`)
	if err != nil {
		return
	}
	defer getBundlePath.Close()

	rows, rerr := getBundlePath.Query(bundleName)
	if err != nil {
		err = rerr
		return
	}
	defer rows.Close()
	if !rows.Next() {
		// no bundlepath set
		return
	}

	var bundlePathSQL sql.NullString
	if err = rows.Scan(&bundlePathSQL); err != nil {
		return
	}

	if bundlePathSQL.Valid {
		bundlePath = bundlePathSQL.String
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
			if nerr := rows.Close(); nerr != nil {
				return nil, nerr
			}
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
	if err := func() error {
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
			if err := s.rmBundle(tx, csvName); err != nil {
				return err
			}
		}

		deletePackage, err := tx.Prepare("DELETE FROM package WHERE package.name=?")
		if err != nil {
			return err
		}
		defer deletePackage.Close()

		if _, err := deletePackage.Exec(packageName); err != nil {
			return err
		}

		deleteChannel, err := tx.Prepare("DELETE FROM channel WHERE package_name = ?")
		if err != nil {
			return err
		}
		defer deleteChannel.Close()

		if _, err := deleteChannel.Exec(packageName); err != nil {
			return err
		}
		return tx.Commit()
	}(); err != nil {
		return err
	}

	// separate transaction so that we remove stranded bundles after the package has been cleared
	return s.RemoveStrandedBundles()
}

func (s *sqlLoader) rmBundle(tx *sql.Tx, csvName string) error {
	deleteBundle, err := tx.Prepare("DELETE FROM operatorbundle WHERE operatorbundle.name=?")
	if err != nil {
		return err
	}
	defer deleteBundle.Close()

	if _, err := deleteBundle.Exec(csvName); err != nil {
		return err
	}

	deleteProvider, err := tx.Prepare("DELETE FROM api_provider WHERE api_provider.operatorbundle_name=?")
	if err != nil {
		return err
	}
	defer deleteProvider.Close()

	if _, err := deleteProvider.Exec(csvName); err != nil {
		return err
	}

	deleteRequirer, err := tx.Prepare("DELETE FROM api_requirer WHERE api_requirer.operatorbundle_name=?")
	if err != nil {
		return err
	}
	defer deleteRequirer.Close()

	if _, err := deleteRequirer.Exec(csvName); err != nil {
		return err
	}

	deleteChannelEntries, err := tx.Prepare("DELETE FROM channel_entry WHERE channel_entry.operatorbundle_name=?")
	if err != nil {
		return err
	}
	defer deleteChannelEntries.Close()

	if _, err := deleteChannelEntries.Exec(csvName); err != nil {
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

	if err := s.rmPackage(tx, manifest.PackageName); err != nil {
		return err
	}

	if err := s.addPackageChannels(tx, manifest); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *sqlLoader) rmPackage(tx *sql.Tx, pkg string) error {
	// Delete package, channel, and entries - they will be recalculated
	deletePkg, err := tx.Prepare("DELETE FROM package WHERE name = ?")
	if err != nil {
		return err
	}

	defer deletePkg.Close()
	_, err = deletePkg.Exec(pkg)
	if err != nil {
		return err
	}

	deleteChan, err := tx.Prepare("DELETE FROM channel WHERE package_name = ?")
	if err != nil {
		return err
	}

	defer deleteChan.Close()
	_, err = deleteChan.Exec(pkg)
	if err != nil {
		return err
	}

	deleteChannelEntries, err := tx.Prepare("DELETE FROM channel_entry WHERE package_name = ?")
	if err != nil {
		return err
	}

	defer deleteChannelEntries.Close()
	_, err = deleteChannelEntries.Exec(pkg)

	return err
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

func (s *sqlLoader) addPackageProperty(tx *sql.Tx, bundleName, pkg, version, bundlePath string) error {
	// Add the package property
	prop := registry.PackageProperty{
		PackageName: pkg,
		Version:     version,
	}
	value, err := json.Marshal(prop)
	if err != nil {
		return err
	}

	return s.addProperty(tx, registry.PackageType, string(value), bundleName, version, bundlePath)
}

func (s *sqlLoader) addBundleProperties(tx *sql.Tx, bundle *registry.Bundle) error {
	type propstring struct {
		Type  string
		Value string
	}
	properties := make(map[propstring]struct{})

	bundleVersion, err := bundle.Version()
	if err != nil {
		return err
	}

	for _, prop := range bundle.Properties {
		value, err := json.Marshal(prop.Value)
		if err != nil {
			return err
		}
		properties[propstring{Type: prop.Type, Value: string(value)}] = struct{}{}
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
		properties[propstring{Type: registry.GVKType, Value: string(value)}] = struct{}{}
	}

	// Add properties from annotations
	csv, err := bundle.ClusterServiceVersion()
	if err != nil {
		// FIXME: Returning nil here is in line with the original implementation, but that was probably wrong. We should probably just bubble-up the error.
		return nil
	}

	if csv == nil {
		// FIXME: Currently, a CSV is requirement of bundle addition. Should this return an error?
		return nil
	}

	var props []registry.Property
	if csv.GetAnnotations() != nil {
		v, ok := csv.GetAnnotations()[registry.PropertyKey]
		if ok {
			if err := json.Unmarshal([]byte(v), &props); err != nil {
				return err
			}
		}
	}

	for _, prop := range props {
		value, err := json.Marshal(&prop.Value)
		if err != nil {
			return err
		}

		// validate if Type is known
		switch prop.Type {
		case registry.LabelType:
			if err := json.Unmarshal(prop.Value, &registry.LabelProperty{}); err != nil {
				return err
			}
		case registry.PackageType:
			if err := json.Unmarshal(prop.Value, &registry.PackageProperty{}); err != nil {
				return err
			}
		case registry.GVKType:
			if err := json.Unmarshal(prop.Value, &registry.GVKProperty{}); err != nil {
				return err
			}
		case registry.DeprecatedType:
			// deprecated has no value
		}

		properties[propstring{Type: prop.Type, Value: string(value)}] = struct{}{}
	}

	// If the bundle has been deprecated before, readd the deprecated property
	deprecated, err := s.deprecated(tx, bundle.Name)
	if err != nil {
		return err
	}
	if deprecated {
		value, err := json.Marshal(registry.DeprecatedProperty{})
		if err != nil {
			return err
		}
		properties[propstring{Type: registry.DeprecatedType, Value: string(value)}] = struct{}{}
	}

	for prop := range properties {
		if err := s.addProperty(tx, prop.Type, prop.Value, bundle.Name, bundleVersion, bundle.BundleImage); err != nil {
			return err
		}
	}

	return nil
}

func (s *sqlLoader) rmChannelEntry(tx *sql.Tx, csvName string) error {
	rows, err := tx.Query(`SELECT entry_id FROM channel_entry WHERE operatorbundle_name=?`, csvName)
	if err != nil {
		return err
	}

	var entryIDs []int64
	for rows.Next() {
		var entryID sql.NullInt64
		rows.Scan(&entryID)
		entryIDs = append(entryIDs, entryID.Int64)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	updateChannelEntry, err := tx.Prepare(`UPDATE channel_entry SET replaces=NULL WHERE replaces=?`)
	if err != nil {
		return err
	}
	for _, id := range entryIDs {
		if _, err := updateChannelEntry.Exec(id); err != nil {
			updateChannelEntry.Close()
			return err
		}
	}
	err = updateChannelEntry.Close()
	if err != nil {
		return err
	}

	_, err = tx.Exec(`DELETE FROM channel WHERE head_operatorbundle_name=?`, csvName)
	if err != nil {
		return err
	}

	return nil
}

func getTailFromBundle(tx *sql.Tx, head string) (bundles []string, err error) {
	getReplacesSkips := `SELECT replaces, skips FROM operatorbundle WHERE name=?`
	isDefaultChannelHead := `SELECT head_operatorbundle_name FROM channel
							INNER JOIN package ON channel.name = package.default_channel
							WHERE channel.head_operatorbundle_name = ?`

	visited := map[string]struct{}{}
	next := []string{head}

	for len(next) > 0 {
		// Pop the next bundle off of the queue
		bundle := next[0]
		next = next[1:] // Potentially inefficient queue implementation, but this function is only used when deprecate is called

		// Check if next is the head of the defaultChannel
		// If it is, the defaultChannel would be removed -- this is not allowed because we cannot know which channel to promote as the new default
		var err error
		if row := tx.QueryRow(isDefaultChannelHead, bundle); row != nil {
			err = row.Scan(&sql.NullString{})
		}
		if err == nil {
			// A nil error indicates that next is the default channel head
			return nil, registry.ErrRemovingDefaultChannelDuringDeprecation
		} else if err != sql.ErrNoRows {
			return nil, err
		}

		rows, err := tx.QueryContext(context.TODO(), getReplacesSkips, bundle)
		if err != nil {
			return nil, err
		}

		var (
			replaces sql.NullString
			skips    sql.NullString
		)
		if rows.Next() {
			if err := rows.Scan(&replaces, &skips); err != nil {
				if nerr := rows.Close(); nerr != nil {
					return nil, nerr
				}
				return nil, err
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if skips.Valid && skips.String != "" {
			for _, skip := range strings.Split(skips.String, ",") {
				if _, ok := visited[skip]; ok {
					// We've already visited this bundle's subgraph
					continue
				}
				visited[skip] = struct{}{}
				next = append(next, skip)
			}
		}
		if replaces.Valid && replaces.String != "" {
			r := replaces.String
			if _, ok := visited[r]; ok {
				// We've already visited this bundle's subgraph
				continue
			}
			visited[r] = struct{}{}
			next = append(next, r)
		}
	}

	// The tail is exactly the set of bundles we visited while traversing the graph from head
	var tail []string
	for v := range visited {
		tail = append(tail, v)
	}

	return tail, nil

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
		if err := s.rmChannelEntry(tx, bundle); err != nil {
			return err
		}
		if err := s.rmBundle(tx, bundle); err != nil {
			return err
		}
	}

	// Remove any channels that start with the deprecated bundle
	_, err = tx.Exec(fmt.Sprintf(`DELETE FROM channel WHERE head_operatorbundle_name="%s"`, name))
	if err != nil {
		return err
	}

	deprecatedValue, err := json.Marshal(registry.DeprecatedProperty{})
	if err != nil {
		return err
	}
	err = s.addProperty(tx, registry.DeprecatedType, string(deprecatedValue), name, version, path)
	if err != nil {
		return err
	}

	// Create a persistent record of the bundle's deprecation
	// This lets us recover from losing the properties and augmented bundle rows
	_, err = tx.Exec("INSERT OR REPLACE INTO deprecated(operatorbundle_name) VALUES(?)", name)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *sqlLoader) RemoveStrandedBundles() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		tx.Rollback()
	}()

	if err := s.rmStrandedBundles(tx); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *sqlLoader) rmStrandedBundles(tx *sql.Tx) error {
	_, err := tx.Exec("DELETE FROM operatorbundle WHERE name NOT IN(select operatorbundle_name from channel_entry)")
	return err
}

func (s *sqlLoader) getBundlesThatSubstitutesFor(tx *sql.Tx, replaces string) ([]string, error) {
	query := `SELECT name FROM operatorbundle WHERE substitutesfor=?`
	rows, err := tx.QueryContext(context.TODO(), query, replaces)
	if err != nil {
		return []string{}, err
	}
	defer rows.Close()

	var substitutesFor []string
	var subsFor sql.NullString
	for rows.Next() {
		if err := rows.Scan(&subsFor); err != nil {
			return []string{}, err
		}
		if subsFor.Valid && subsFor.String != "" {
			substitutesFor = append(substitutesFor, subsFor.String)
		}
	}
	return substitutesFor, nil
}

func (s *sqlLoader) getBundleSubstitution(tx *sql.Tx, name string) (string, error) {
	query := `SELECT substitutesfor FROM operatorbundle WHERE name=?`
	rows, err := tx.QueryContext(context.TODO(), query, name)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var substitutesFor sql.NullString
	if rows.Next() {
		if err := rows.Scan(&substitutesFor); err != nil {
			return "", err
		}
	}
	return substitutesFor.String, nil
}

func (s *sqlLoader) deprecated(tx *sql.Tx, name string) (bool, error) {
	var err error
	if row := tx.QueryRow(`SELECT * FROM deprecated WHERE operatorbundle_name = ?`, name); row != nil {
		err = row.Scan(&sql.NullString{})
	}
	if err == sql.ErrNoRows {
		return false, nil
	}

	// Ignore any deprecated bundles
	return err == nil, err
}
