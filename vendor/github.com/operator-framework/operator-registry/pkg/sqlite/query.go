package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

type SQLQuerier struct {
	db *sql.DB
}

var _ registry.Query = &SQLQuerier{}

func NewSQLLiteQuerier(dbFilename string) (*SQLQuerier, error) {
	db, err := sql.Open("sqlite3", "file:"+dbFilename+"?immutable=true")
	if err != nil {
		return nil, err
	}

	return &SQLQuerier{db}, nil
}

func NewSQLLiteQuerierFromDb(db *sql.DB) *SQLQuerier {
	return &SQLQuerier{db}
}

func (s *SQLQuerier) ListTables(ctx context.Context) ([]string, error) {
	query := "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := []string{}
	for rows.Next() {
		var tableName sql.NullString
		if err := rows.Scan(&tableName); err != nil {
			return nil, err
		}
		if tableName.Valid {
			tables = append(tables, tableName.String)
		}
	}
	return tables, nil
}

// ListPackages returns a list of package names as strings
func (s *SQLQuerier) ListPackages(ctx context.Context) ([]string, error) {
	query := "SELECT DISTINCT name FROM package"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	packages := []string{}
	for rows.Next() {
		var pkgName sql.NullString
		if err := rows.Scan(&pkgName); err != nil {
			return nil, err
		}
		if pkgName.Valid {
			packages = append(packages, pkgName.String)
		}
	}
	return packages, nil
}

func (s *SQLQuerier) GetPackage(ctx context.Context, name string) (*registry.PackageManifest, error) {
	query := `SELECT DISTINCT package.name, default_channel, channel.name, channel.head_operatorbundle_name
              FROM package INNER JOIN channel ON channel.package_name=package.name
              WHERE package.name=?`
	rows, err := s.db.QueryContext(ctx, query, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pkgName sql.NullString
	var defaultChannel sql.NullString
	var channelName sql.NullString
	var bundleName sql.NullString
	if !rows.Next() {
		return nil, fmt.Errorf("package %s not found", name)
	}
	if err := rows.Scan(&pkgName, &defaultChannel, &channelName, &bundleName); err != nil {
		return nil, err
	}
	pkg := &registry.PackageManifest{
		PackageName:        pkgName.String,
		DefaultChannelName: defaultChannel.String,
		Channels: []registry.PackageChannel{
			{
				Name:           channelName.String,
				CurrentCSVName: bundleName.String,
			},
		},
	}

	for rows.Next() {
		if err := rows.Scan(&pkgName, &defaultChannel, &channelName, &bundleName); err != nil {
			return nil, err
		}
		pkg.Channels = append(pkg.Channels, registry.PackageChannel{Name: channelName.String, CurrentCSVName: bundleName.String})
	}
	return pkg, nil
}

func (s *SQLQuerier) GetDefaultPackage(ctx context.Context, name string) (string, error) {
	query := `SELECT default_channel
              FROM package WHERE package.name=?`
	rows, err := s.db.QueryContext(ctx, query, name)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var defaultChannel sql.NullString
	if !rows.Next() {
		return "", fmt.Errorf("package %s not found", name)
	}
	if err := rows.Scan(&defaultChannel); err != nil {
		return "", err
	}

	if !defaultChannel.Valid {
		return "", fmt.Errorf("default channel not valid")
	}

	return defaultChannel.String, nil
}

func (s *SQLQuerier) GetChannelEntriesFromPackage(ctx context.Context, packageName string) ([]registry.ChannelEntryAnnotated, error) {
	query := `SELECT channel_entry.package_name, channel_entry.channel_name, channel_entry.operatorbundle_name, op_bundle.version, op_bundle.bundlepath, replaces.operatorbundle_name, replacesbundle.version, replacesbundle.bundlepath
			  FROM channel_entry
			  LEFT JOIN channel_entry replaces ON channel_entry.replaces = replaces.entry_id
			  LEFT JOIN operatorbundle op_bundle ON channel_entry.operatorbundle_name = op_bundle.name
			  LEFT JOIN operatorbundle replacesbundle ON replaces.operatorbundle_name = replacesbundle.name
              WHERE channel_entry.package_name = ?;`

	var entries []registry.ChannelEntryAnnotated
	rows, err := s.db.QueryContext(ctx, query, packageName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pkgName sql.NullString
	var channelName sql.NullString
	var bundleName sql.NullString
	var replaces sql.NullString
	var version sql.NullString
	var bundlePath sql.NullString
	var replacesVersion sql.NullString
	var replacesBundlePath sql.NullString

	for rows.Next() {
		if err := rows.Scan(&pkgName, &channelName, &bundleName, &version, &bundlePath, &replaces, &replacesVersion, &replacesBundlePath); err != nil {
			return nil, err
		}

		channelEntryNode := registry.ChannelEntryAnnotated{
			PackageName:        pkgName.String,
			ChannelName:        channelName.String,
			BundleName:         bundleName.String,
			Version:            version.String,
			BundlePath:         bundlePath.String,
			Replaces:           replaces.String,
			ReplacesVersion:    replacesVersion.String,
			ReplacesBundlePath: replacesBundlePath.String,
		}

		entries = append(entries, channelEntryNode)
	}

	return entries, nil
}

func (s *SQLQuerier) GetBundle(ctx context.Context, pkgName, channelName, csvName string) (*api.Bundle, error) {
	query := `SELECT DISTINCT channel_entry.entry_id, operatorbundle.name, operatorbundle.bundle, operatorbundle.bundlepath, operatorbundle.version, operatorbundle.skiprange
			  FROM operatorbundle INNER JOIN channel_entry ON operatorbundle.name=channel_entry.operatorbundle_name
              WHERE channel_entry.package_name=? AND channel_entry.channel_name=? AND operatorbundle_name=? LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, pkgName, channelName, csvName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no entry found for %s %s %s", pkgName, channelName, csvName)
	}
	var entryId sql.NullInt64
	var name sql.NullString
	var bundle sql.NullString
	var bundlePath sql.NullString
	var version sql.NullString
	var skipRange sql.NullString
	if err := rows.Scan(&entryId, &name, &bundle, &bundlePath, &version, &skipRange); err != nil {
		return nil, err
	}

	out := &api.Bundle{}
	if bundle.Valid && bundle.String != "" {
		out, err = registry.BundleStringToAPIBundle(bundle.String)
		if err != nil {
			return nil, err
		}
	}
	out.CsvName = name.String
	out.PackageName = pkgName
	out.ChannelName = channelName
	out.BundlePath = bundlePath.String
	out.Version = version.String
	out.SkipRange = skipRange.String

	provided, required, err := s.GetApisForEntry(ctx, entryId.Int64)
	if err != nil {
		return nil, err
	}
	out.ProvidedApis = provided
	out.RequiredApis = required

	dependencies, err := s.GetDependenciesForBundle(ctx, name.String, version.String, bundlePath.String)
	if err != nil {
		return nil, err
	}
	out.Dependencies = dependencies

	properties, err := s.GetPropertiesForBundle(ctx, name.String, version.String, bundlePath.String)
	if err != nil {
		return nil, err
	}
	out.Properties = properties

	return out, nil
}

func (s *SQLQuerier) GetBundleForChannel(ctx context.Context, pkgName string, channelName string) (*api.Bundle, error) {
	query := `SELECT DISTINCT channel_entry.entry_id, operatorbundle.name, operatorbundle.bundle, operatorbundle.bundlepath, operatorbundle.version, operatorbundle.skiprange FROM channel
              INNER JOIN operatorbundle ON channel.head_operatorbundle_name=operatorbundle.name
              INNER JOIN channel_entry ON (channel_entry.channel_name = channel.name and channel_entry.package_name=channel.package_name and channel_entry.operatorbundle_name=operatorbundle.name)
              WHERE channel.package_name=? AND channel.name=? LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, pkgName, channelName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no entry found for %s %s", pkgName, channelName)
	}
	var entryId sql.NullInt64
	var name sql.NullString
	var bundle sql.NullString
	var bundlePath sql.NullString
	var version sql.NullString
	var skipRange sql.NullString
	if err := rows.Scan(&entryId, &name, &bundle, &bundlePath, &version, &skipRange); err != nil {
		return nil, err
	}

	out := &api.Bundle{}
	if bundle.Valid && bundle.String != "" {
		out, err = registry.BundleStringToAPIBundle(bundle.String)
		if err != nil {
			return nil, err
		}
	}
	out.CsvName = name.String
	out.PackageName = pkgName
	out.ChannelName = channelName
	out.BundlePath = bundlePath.String
	out.Version = version.String
	out.SkipRange = skipRange.String

	provided, required, err := s.GetApisForEntry(ctx, entryId.Int64)
	if err != nil {
		return nil, err
	}
	out.ProvidedApis = provided
	out.RequiredApis = required

	dependencies, err := s.GetDependenciesForBundle(ctx, name.String, version.String, bundlePath.String)
	if err != nil {
		return nil, err
	}
	out.Dependencies = dependencies

	properties, err := s.GetPropertiesForBundle(ctx, name.String, version.String, bundlePath.String)
	if err != nil {
		return nil, err
	}
	out.Properties = properties

	return out, nil
}

func (s *SQLQuerier) GetChannelEntriesThatReplace(ctx context.Context, name string) (entries []*registry.ChannelEntry, err error) {
	query := `SELECT DISTINCT channel_entry.package_name, channel_entry.channel_name, channel_entry.operatorbundle_name
			  FROM channel_entry
			  LEFT OUTER JOIN channel_entry replaces ON channel_entry.replaces = replaces.entry_id
              WHERE replaces.operatorbundle_name = ?`
	rows, err := s.db.QueryContext(ctx, query, name)
	if err != nil {
		return
	}
	defer rows.Close()

	entries = []*registry.ChannelEntry{}

	for rows.Next() {
		var pkgNameSQL sql.NullString
		var channelNameSQL sql.NullString
		var bundleNameSQL sql.NullString

		if err = rows.Scan(&pkgNameSQL, &channelNameSQL, &bundleNameSQL); err != nil {
			return
		}
		entries = append(entries, &registry.ChannelEntry{
			PackageName: pkgNameSQL.String,
			ChannelName: channelNameSQL.String,
			BundleName:  bundleNameSQL.String,
			Replaces:    name,
		})
	}
	if len(entries) == 0 {
		err = fmt.Errorf("no channel entries found that replace %s", name)
		return
	}
	return
}

func (s *SQLQuerier) GetBundleThatReplaces(ctx context.Context, name, pkgName, channelName string) (*api.Bundle, error) {
	query := `SELECT DISTINCT replaces.entry_id, operatorbundle.name, operatorbundle.bundle, operatorbundle.bundlepath, operatorbundle.version, operatorbundle.skiprange
              FROM channel_entry
			  LEFT  OUTER JOIN channel_entry replaces ON replaces.replaces = channel_entry.entry_id
			  INNER JOIN operatorbundle ON replaces.operatorbundle_name = operatorbundle.name
			  WHERE channel_entry.operatorbundle_name = ? AND channel_entry.package_name = ? AND channel_entry.channel_name = ? LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, name, pkgName, channelName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no entry found for %s %s", pkgName, channelName)
	}
	var entryId sql.NullInt64
	var outName sql.NullString
	var bundle sql.NullString
	var bundlePath sql.NullString
	var version sql.NullString
	var skipRange sql.NullString
	if err := rows.Scan(&entryId, &outName, &bundle, &bundlePath, &version, &skipRange); err != nil {
		return nil, err
	}

	out := &api.Bundle{}
	if bundle.Valid && bundle.String != "" {
		out, err = registry.BundleStringToAPIBundle(bundle.String)
		if err != nil {
			return nil, err
		}
	}
	out.CsvName = outName.String
	out.PackageName = pkgName
	out.ChannelName = channelName
	out.BundlePath = bundlePath.String
	out.Version = version.String
	out.SkipRange = skipRange.String

	provided, required, err := s.GetApisForEntry(ctx, entryId.Int64)
	if err != nil {
		return nil, err
	}
	out.ProvidedApis = provided
	out.RequiredApis = required

	dependencies, err := s.GetDependenciesForBundle(ctx, outName.String, version.String, bundlePath.String)
	if err != nil {
		return nil, err
	}
	out.Dependencies = dependencies

	properties, err := s.GetPropertiesForBundle(ctx, outName.String, version.String, bundlePath.String)
	if err != nil {
		return nil, err
	}
	out.Properties = properties

	return out, nil
}

func (s *SQLQuerier) GetChannelEntriesThatProvide(ctx context.Context, group, version, kind string) (entries []*registry.ChannelEntry, err error) {
	// TODO: join on full fk, not just operatorbundlename
	query := `SELECT DISTINCT channel_entry.package_name, channel_entry.channel_name, channel_entry.operatorbundle_name, replaces.operatorbundle_name
          FROM channel_entry
          INNER JOIN properties ON channel_entry.operatorbundle_name = properties.operatorbundle_name
          LEFT OUTER JOIN channel_entry replaces ON channel_entry.replaces = replaces.entry_id
		  WHERE properties.type=? AND properties.value=?`

	value, err := json.Marshal(map[string]string{
		"group":   group,
		"version": version,
		"kind":    kind,
	})
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, registry.GVKType, string(value))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries = []*registry.ChannelEntry{}

	for rows.Next() {
		var pkgNameSQL sql.NullString
		var channelNameSQL sql.NullString
		var bundleNameSQL sql.NullString
		var replacesSQL sql.NullString
		if err = rows.Scan(&pkgNameSQL, &channelNameSQL, &bundleNameSQL, &replacesSQL); err != nil {
			return
		}

		entries = append(entries, &registry.ChannelEntry{
			PackageName: pkgNameSQL.String,
			ChannelName: channelNameSQL.String,
			BundleName:  bundleNameSQL.String,
			Replaces:    replacesSQL.String,
		})
	}
	if len(entries) == 0 {
		err = fmt.Errorf("no channel entries found that provide %s %s %s", group, version, kind)
		return
	}
	return
}

// Get latest channel entries that provide an api
func (s *SQLQuerier) GetLatestChannelEntriesThatProvide(ctx context.Context, group, version, kind string) (entries []*registry.ChannelEntry, err error) {
	query := `SELECT DISTINCT channel_entry.package_name, channel_entry.channel_name, channel_entry.operatorbundle_name, replaces.operatorbundle_name, MIN(channel_entry.depth)
          FROM channel_entry
          INNER JOIN properties ON channel_entry.operatorbundle_name = properties.operatorbundle_name
		  LEFT OUTER JOIN channel_entry replaces ON channel_entry.replaces = replaces.entry_id
		  WHERE properties.type = ? AND properties.value = ?
		  GROUP BY channel_entry.package_name, channel_entry.channel_name`

	value, err := json.Marshal(map[string]string{
		"group":   group,
		"version": version,
		"kind":    kind,
	})
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, query, registry.GVKType, string(value))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries = []*registry.ChannelEntry{}

	for rows.Next() {
		var pkgNameSQL sql.NullString
		var channelNameSQL sql.NullString
		var bundleNameSQL sql.NullString
		var replacesSQL sql.NullString
		var min_depth sql.NullInt64
		if err = rows.Scan(&pkgNameSQL, &channelNameSQL, &bundleNameSQL, &replacesSQL, &min_depth); err != nil {
			return nil, err
		}

		entries = append(entries, &registry.ChannelEntry{
			PackageName: pkgNameSQL.String,
			ChannelName: channelNameSQL.String,
			BundleName:  bundleNameSQL.String,
			Replaces:    replacesSQL.String,
		})
	}
	if len(entries) == 0 {
		err = fmt.Errorf("no channel entries found that provide %s %s %s", group, version, kind)
		return nil, err
	}
	return entries, nil
}

// Get the the latest bundle that provides the API in a default channel, error unless there is ONLY one
func (s *SQLQuerier) GetBundleThatProvides(ctx context.Context, group, apiVersion, kind string) (*api.Bundle, error) {
	query := `SELECT DISTINCT channel_entry.entry_id, operatorbundle.bundle, operatorbundle.bundlepath, MIN(channel_entry.depth), channel_entry.operatorbundle_name, channel_entry.package_name, channel_entry.channel_name, channel_entry.replaces, operatorbundle.version, operatorbundle.skiprange
          FROM channel_entry
		  INNER JOIN operatorbundle ON operatorbundle.name = channel_entry.operatorbundle_name
		  INNER JOIN properties ON channel_entry.operatorbundle_name = properties.operatorbundle_name
		  INNER JOIN package ON package.name = channel_entry.package_name
		  WHERE properties.type = ? AND properties.value = ? AND package.default_channel = channel_entry.channel_name
		  GROUP BY channel_entry.package_name, channel_entry.channel_name`

	value, err := json.Marshal(map[string]string{
		"group":   group,
		"version": apiVersion,
		"kind":    kind,
	})
	rows, err := s.db.QueryContext(ctx, query, registry.GVKType, string(value))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no entry found that provides %s %s %s", group, apiVersion, kind)
	}
	var entryId sql.NullInt64
	var bundle sql.NullString
	var bundlePath sql.NullString
	var min_depth sql.NullInt64
	var bundleName sql.NullString
	var pkgName sql.NullString
	var channelName sql.NullString
	var replaces sql.NullString
	var version sql.NullString
	var skipRange sql.NullString
	if err := rows.Scan(&entryId, &bundle, &bundlePath, &min_depth, &bundleName, &pkgName, &channelName, &replaces, &version, &skipRange); err != nil {
		return nil, err
	}

	if !bundle.Valid {
		return nil, fmt.Errorf("no entry found that provides %s %s %s", group, apiVersion, kind)
	}

	out := &api.Bundle{}
	if bundle.Valid && bundle.String != "" {
		out, err = registry.BundleStringToAPIBundle(bundle.String)
		if err != nil {
			return nil, err
		}
	}
	out.CsvName = bundleName.String
	out.PackageName = pkgName.String
	out.ChannelName = channelName.String
	out.BundlePath = bundlePath.String
	out.Version = version.String
	out.SkipRange = skipRange.String

	provided, required, err := s.GetApisForEntry(ctx, entryId.Int64)
	if err != nil {
		return nil, err
	}
	out.ProvidedApis = provided
	out.RequiredApis = required

	dependencies, err := s.GetDependenciesForBundle(ctx, bundleName.String, version.String, bundlePath.String)
	if err != nil {
		return nil, err
	}
	out.Dependencies = dependencies

	properties, err := s.GetPropertiesForBundle(ctx, bundleName.String, version.String, bundlePath.String)
	if err != nil {
		return nil, err
	}
	out.Properties = properties

	return out, nil
}

func (s *SQLQuerier) ListImages(ctx context.Context) ([]string, error) {
	query := "SELECT DISTINCT image FROM related_image"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	images := []string{}
	for rows.Next() {
		var imgName sql.NullString
		if err := rows.Scan(&imgName); err != nil {
			return nil, err
		}
		if imgName.Valid {
			images = append(images, imgName.String)
		}
	}
	return images, nil
}

func (s *SQLQuerier) GetImagesForBundle(ctx context.Context, csvName string) ([]string, error) {
	query := "SELECT DISTINCT image FROM related_image WHERE operatorbundle_name=?"
	rows, err := s.db.QueryContext(ctx, query, csvName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	images := []string{}
	for rows.Next() {
		var imgName sql.NullString
		if err := rows.Scan(&imgName); err != nil {
			return nil, err
		}
		if imgName.Valid {
			images = append(images, imgName.String)
		}
	}
	return images, nil
}

func (s *SQLQuerier) GetApisForEntry(ctx context.Context, entryID int64) (provided []*api.GroupVersionKind, required []*api.GroupVersionKind, err error) {
	groups := map[string]struct{}{}
	kinds := map[string]struct{}{}
	versions := map[string]struct{}{}

	providedQuery := `SELECT properties.value FROM properties 
		 	  		  INNER JOIN channel_entry ON channel_entry.operatorbundle_name = properties.operatorbundle_name
			  		  WHERE properties.type=? AND channel_entry.entry_id=?`

	providedRows, err := s.db.QueryContext(ctx, providedQuery, registry.GVKType, entryID)
	if err != nil {
		return nil, nil, err
	}
	defer providedRows.Close()

	provided = []*api.GroupVersionKind{}
	for providedRows.Next() {
		var value sql.NullString

		if err := providedRows.Scan(&value); err != nil {
			return nil, nil, err
		}

		if !value.Valid {
			continue
		}
		prop := registry.GVKProperty{}
		if err := json.Unmarshal([]byte(value.String), &prop); err != nil {
			continue
		}

		provided = append(provided, &api.GroupVersionKind{
			Group:   prop.Group,
			Version: prop.Version,
			Kind:    prop.Kind,
		})
		groups[prop.Group] = struct{}{}
		versions[prop.Version] = struct{}{}
		kinds[prop.Kind] = struct{}{}
	}

	requiredQuery := `SELECT DISTINCT dependencies.value FROM dependencies
					  INNER JOIN channel_entry ON channel_entry.operatorbundle_name = dependencies.operatorbundle_name
					  WHERE dependencies.type=? AND channel_entry.entry_id=?`

	requiredRows, err := s.db.QueryContext(ctx, requiredQuery, registry.GVKType, entryID)
	if err != nil {
		return nil, nil, err
	}
	defer requiredRows.Close()

	required = []*api.GroupVersionKind{}
	for requiredRows.Next() {
		var value sql.NullString

		if err := requiredRows.Scan(&value); err != nil {
			return nil, nil, err
		}
		if !value.Valid {
			continue
		}
		dep := registry.GVKDependency{}
		if err := json.Unmarshal([]byte(value.String), &dep); err != nil {
			continue
		}

		required = append(required, &api.GroupVersionKind{
			Group:   dep.Group,
			Version: dep.Version,
			Kind:    dep.Kind,
		})
		groups[dep.Group] = struct{}{}
		versions[dep.Version] = struct{}{}
		kinds[dep.Kind] = struct{}{}
	}

	argsFor := func(s map[string]struct{}) string {
		l := []string{}
		for v := range s {
			l = append(l, "\""+v+"\"")
		}
		return "(" + strings.Join(l, ",") + ")"
	}

	pluralQuery := `SELECT * FROM api` +
		` WHERE api.group_name IN ` + argsFor(groups) +
		` AND api.version IN ` + argsFor(versions) +
		` AND api.kind IN ` + argsFor(kinds)

	pluralRows, err := s.db.QueryContext(ctx, pluralQuery)
	if err != nil {
		return nil, nil, err
	}
	defer pluralRows.Close()

	gvkToPlural := map[registry.GVKProperty]string{}
	for pluralRows.Next() {
		var groupName sql.NullString
		var versionName sql.NullString
		var kindName sql.NullString
		var pluralName sql.NullString

		if err := pluralRows.Scan(&groupName, &versionName, &kindName, &pluralName); err != nil {
			continue
		}
		if !groupName.Valid || !versionName.Valid || !kindName.Valid || !pluralName.Valid {
			continue
		}
		gvkToPlural[registry.GVKProperty{
			Group:   groupName.String,
			Version: versionName.String,
			Kind:    kindName.String,
		}] = pluralName.String
	}

	for i, p := range provided {
		if p.Plural != "" {
			continue
		}
		plural, ok := gvkToPlural[registry.GVKProperty{
			Group:   p.Group,
			Version: p.Version,
			Kind:    p.Kind,
		}]
		if !ok {
			continue
		}
		provided[i].Plural = plural
	}
	for i, r := range required {
		if r.Plural != "" {
			continue
		}
		plural, ok := gvkToPlural[registry.GVKProperty{
			Group:   r.Group,
			Version: r.Version,
			Kind:    r.Kind,
		}]
		if !ok {
			continue
		}
		required[i].Plural = plural
	}
	return
}

func (s *SQLQuerier) GetBundleVersion(ctx context.Context, image string) (string, error) {
	query := `SELECT version FROM operatorbundle WHERE bundlepath=? LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, image)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var version sql.NullString
	if rows.Next() {
		if err := rows.Scan(&version); err != nil {
			return "", err
		}
	}
	if version.Valid {
		return version.String, nil
	}
	return "", fmt.Errorf("bundle %s not found", image)
}

func (s *SQLQuerier) GetBundlePathsForPackage(ctx context.Context, pkgName string) ([]string, error) {
	query := `SELECT DISTINCT bundlepath FROM operatorbundle
	INNER JOIN channel_entry ON operatorbundle.name=channel_entry.operatorbundle_name
	WHERE channel_entry.package_name=?`
	rows, err := s.db.QueryContext(ctx, query, pkgName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	images := []string{}
	for rows.Next() {
		var imgName sql.NullString
		if err := rows.Scan(&imgName); err != nil {
			return nil, err
		}
		if imgName.Valid && imgName.String != "" {
			images = append(images, imgName.String)
		} else {
			return nil, fmt.Errorf("Index malformed: cannot find paths to bundle images")
		}
	}
	return images, nil
}

func (s *SQLQuerier) GetBundlesForPackage(ctx context.Context, pkgName string) (map[registry.BundleKey]struct{}, error) {
	query := `SELECT DISTINCT name, bundlepath, version FROM operatorbundle
	INNER JOIN channel_entry ON operatorbundle.name=channel_entry.operatorbundle_name
	WHERE channel_entry.package_name=?`
	rows, err := s.db.QueryContext(ctx, query, pkgName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bundles := map[registry.BundleKey]struct{}{}
	for rows.Next() {
		var name sql.NullString
		var bundlepath sql.NullString
		var version sql.NullString
		if err := rows.Scan(&name, &bundlepath, &version); err != nil {
			return nil, err
		}
		key := registry.BundleKey{}
		if name.Valid && name.String != "" {
			key.CsvName = name.String
		}
		if bundlepath.Valid && bundlepath.String != "" {
			key.BundlePath = bundlepath.String
		}
		if version.Valid && version.String != "" {
			key.Version = version.String
		}
		if key.IsEmpty() {
			return nil, fmt.Errorf("Index malformed: cannot find identifier for bundle in package %s", pkgName)
		}
		bundles[key] = struct{}{}
	}
	return bundles, nil
}

func (s *SQLQuerier) GetDefaultChannelForPackage(ctx context.Context, pkgName string) (string, error) {
	query := `SELECT DISTINCT default_channel FROM package WHERE name=? LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, pkgName)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var defaultChannel sql.NullString
	if rows.Next() {
		if err := rows.Scan(&defaultChannel); err != nil {
			return "", err
		}
	}
	if defaultChannel.Valid {
		return defaultChannel.String, nil
	}
	return "", nil
}

func (s *SQLQuerier) ListChannels(ctx context.Context, pkgName string) ([]string, error) {
	query := `SELECT DISTINCT name FROM channel WHERE channel.package_name=?`
	rows, err := s.db.QueryContext(ctx, query, pkgName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := []string{}
	for rows.Next() {
		var chName sql.NullString
		if err := rows.Scan(&chName); err != nil {
			return nil, err
		}
		if chName.Valid {
			channels = append(channels, chName.String)
		}
	}
	return channels, nil
}

func (s *SQLQuerier) GetCurrentCSVNameForChannel(ctx context.Context, pkgName, channel string) (string, error) {
	query := `SELECT DISTINCT head_operatorbundle_name FROM channel WHERE channel.package_name=? AND channel.name=?`
	rows, err := s.db.QueryContext(ctx, query, pkgName, channel)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var csvName sql.NullString
	if rows.Next() {
		if err := rows.Scan(&csvName); err != nil {
			return "", err
		}
	}
	if csvName.Valid {
		return csvName.String, nil
	}
	return "", nil
}

func (s *SQLQuerier) ListBundles(ctx context.Context) (bundles []*api.Bundle, err error) {
	query := `SELECT DISTINCT channel_entry.entry_id, operatorbundle.bundle, operatorbundle.bundlepath,
	channel_entry.operatorbundle_name, channel_entry.package_name, channel_entry.channel_name, operatorbundle.replaces, operatorbundle.skips,
	operatorbundle.version, operatorbundle.skiprange,
	dependencies.type, dependencies.value,
	properties.type, properties.value
	FROM channel_entry
	INNER JOIN operatorbundle ON operatorbundle.name = channel_entry.operatorbundle_name
	LEFT OUTER JOIN dependencies ON dependencies.operatorbundle_name = channel_entry.operatorbundle_name
	LEFT OUTER JOIN properties ON properties.operatorbundle_name = channel_entry.operatorbundle_name
	INNER JOIN package ON package.name = channel_entry.package_name`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bundles = []*api.Bundle{}
	bundlesMap := map[string]*api.Bundle{}
	for rows.Next() {
		var entryID sql.NullInt64
		var bundle sql.NullString
		var bundlePath sql.NullString
		var bundleName sql.NullString
		var pkgName sql.NullString
		var channelName sql.NullString
		var replaces sql.NullString
		var skips sql.NullString
		var version sql.NullString
		var skipRange sql.NullString
		var depType sql.NullString
		var depValue sql.NullString
		var propType sql.NullString
		var propValue sql.NullString
		if err := rows.Scan(&entryID, &bundle, &bundlePath, &bundleName, &pkgName, &channelName, &replaces, &skips, &version, &skipRange, &depType, &depValue, &propType, &propValue); err != nil {
			return nil, err
		}

		bundleKey := fmt.Sprintf("%s/%s/%s", bundleName.String, version.String, bundlePath.String)
		bundleItem, ok := bundlesMap[bundleKey]
		if ok {
			// Create new dependency object
			dep := &api.Dependency{}
			if !depType.Valid || !depValue.Valid {
				continue
			}
			dep.Type = depType.String
			dep.Value = depValue.String

			// Add new dependency to the existing list
			existingDeps := bundleItem.Dependencies
			existingDeps = append(existingDeps, dep)
			bundleItem.Dependencies = existingDeps

			// Create new property object
			prop := &api.Property{}
			if !propType.Valid || !propValue.Valid {
				continue
			}
			prop.Type = propType.String
			prop.Value = propValue.String

			// Add new property to the existing list
			existingProps := bundleItem.Properties
			existingProps = append(existingProps, prop)
			bundleItem.Properties = existingProps
		} else {
			// Create new bundle
			out := &api.Bundle{}
			if bundle.Valid && bundle.String != "" {
				out, err = registry.BundleStringToAPIBundle(bundle.String)
				if err != nil {
					return nil, err
				}
			}

			out.CsvName = bundleName.String
			out.PackageName = pkgName.String
			out.ChannelName = channelName.String
			out.BundlePath = bundlePath.String
			out.Version = version.String
			out.SkipRange = skipRange.String
			out.Replaces = replaces.String
			out.Skips = strings.Split(skips.String, ",")

			provided, required, err := s.GetApisForEntry(ctx, entryID.Int64)
			if err != nil {
				return nil, err
			}
			out.ProvidedApis = provided
			out.RequiredApis = required

			// Create new dependency and dependency list
			dep := &api.Dependency{}
			dependencies := []*api.Dependency{}
			dep.Type = depType.String
			dep.Value = depValue.String
			dependencies = append(dependencies, dep)
			out.Dependencies = dependencies

			// Create new property and property list
			prop := &api.Property{}
			properties := []*api.Property{}
			prop.Type = propType.String
			prop.Value = propValue.String
			properties = append(properties, prop)
			out.Properties = properties

			bundlesMap[bundleKey] = out
		}
	}

	for _, v := range bundlesMap {
		if len(v.Dependencies) > 1 {
			newDeps := unique(v.Dependencies)
			v.Dependencies = newDeps
		}
		if len(v.Properties) > 1 {
			newProps := uniqueProps(v.Properties)
			v.Properties = newProps
		}
		bundles = append(bundles, v)
	}

	return
}

func unique(deps []*api.Dependency) []*api.Dependency {
	keys := make(map[string]bool)
	list := []*api.Dependency{}
	for _, entry := range deps {
		depKey := fmt.Sprintf("%s/%s", entry.Type, entry.Value)
		if _, value := keys[depKey]; !value {
			keys[depKey] = true
			list = append(list, entry)
		}
	}
	return list
}

func uniqueProps(props []*api.Property) []*api.Property {
	keys := make(map[string]bool)
	list := []*api.Property{}
	for _, entry := range props {
		propKey := fmt.Sprintf("%s/%s", entry.Type, entry.Value)
		if _, value := keys[propKey]; !value {
			keys[propKey] = true
			list = append(list, entry)
		}
	}
	return list
}

func (s *SQLQuerier) GetDependenciesForBundle(ctx context.Context, name, version, path string) (dependencies []*api.Dependency, err error) {
	depQuery := `SELECT DISTINCT type, value FROM dependencies
	WHERE operatorbundle_name=?
	AND (operatorbundle_version=? OR operatorbundle_version is NULL)
	AND (operatorbundle_path=? OR operatorbundle_path is NULL)`

	rows, err := s.db.QueryContext(ctx, depQuery, name, version, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dependencies = []*api.Dependency{}
	for rows.Next() {
		var typeName sql.NullString
		var value sql.NullString

		if err := rows.Scan(&typeName, &value); err != nil {
			return nil, err
		}
		if !typeName.Valid || !value.Valid {
			return nil, err
		}
		dependencies = append(dependencies, &api.Dependency{
			Type:  typeName.String,
			Value: value.String,
		})
	}

	return
}

func (s *SQLQuerier) GetPropertiesForBundle(ctx context.Context, name, version, path string) (properties []*api.Property, err error) {
	propQuery := `SELECT DISTINCT type, value FROM properties 
				 WHERE operatorbundle_name=?
				 AND (operatorbundle_version=? OR operatorbundle_version is NULL)
				 AND (operatorbundle_path=? OR operatorbundle_path is NULL)`

	rows, err := s.db.QueryContext(ctx, propQuery, name, version, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	properties = []*api.Property{}
	for rows.Next() {
		var typeName sql.NullString
		var value sql.NullString

		if err := rows.Scan(&typeName, &value); err != nil {
			return nil, err
		}
		if !typeName.Valid || !value.Valid {
			return nil, err
		}
		properties = append(properties, &api.Property{
			Type:  typeName.String,
			Value: value.String,
		})
	}

	return
}
