package sqlite

import (
	"context"
	"database/sql"
	"fmt"

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

func (s *SQLQuerier) GetBundle(ctx context.Context, pkgName, channelName, csvName string) (*api.Bundle, error) {
	query := `SELECT DISTINCT channel_entry.entry_id, operatorbundle.name, operatorbundle.bundle, operatorbundle.bundlepath 
			  FROM operatorbundle INNER JOIN channel_entry ON operatorbundle.name=channel_entry.operatorbundle_name
              WHERE channel_entry.package_name=? AND channel_entry.channel_name=? AND operatorbundle_name=? LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, pkgName, channelName, csvName)
	if err != nil {
		return nil, err
	}

	if !rows.Next() {
		return nil,  fmt.Errorf("no entry found for %s %s %s", pkgName, channelName, csvName)
	}
	var entryId sql.NullInt64
	var name sql.NullString
	var bundle sql.NullString
	var bundlePath sql.NullString
	if err := rows.Scan(&entryId, &name, &bundle, &bundlePath); err != nil {
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

	provided, required, err := s.GetApisForEntry(ctx, entryId.Int64)
	if err != nil {
		return nil, err
	}
	out.ProvidedApis = provided
	out.RequiredApis = required

	return out, nil
}

func (s *SQLQuerier) GetBundleForChannel(ctx context.Context, pkgName string, channelName string) (*api.Bundle, error) {
	query := `SELECT DISTINCT channel_entry.entry_id, operatorbundle.name, operatorbundle.bundle, operatorbundle.bundlepath FROM channel 
              INNER JOIN operatorbundle ON channel.head_operatorbundle_name=operatorbundle.name
              INNER JOIN channel_entry ON (channel_entry.channel_name = channel.name and channel_entry.package_name=channel.package_name and channel_entry.operatorbundle_name=operatorbundle.name)
              WHERE channel.package_name=? AND channel.name=? LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, pkgName, channelName)
	if err != nil {
		return nil, err
	}

	if !rows.Next() {
		return nil,  fmt.Errorf("no entry found for %s %s", pkgName, channelName)
	}
	var entryId sql.NullInt64
	var name sql.NullString
	var bundle sql.NullString
	var bundlePath sql.NullString
	if err := rows.Scan(&entryId, &name, &bundle, &bundlePath); err != nil {
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

	provided, required, err := s.GetApisForEntry(ctx, entryId.Int64)
	if err != nil {
		return nil, err
	}
	out.ProvidedApis = provided
	out.RequiredApis = required

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
	query := `SELECT DISTINCT replaces.entry_id, operatorbundle.name, operatorbundle.bundle, operatorbundle.bundlepath
              FROM channel_entry
			  LEFT  OUTER JOIN channel_entry replaces ON replaces.replaces = channel_entry.entry_id
			  INNER JOIN operatorbundle ON replaces.operatorbundle_name = operatorbundle.name
			  WHERE channel_entry.operatorbundle_name = ? AND channel_entry.package_name = ? AND channel_entry.channel_name = ? LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, name, pkgName, channelName)
	if err != nil {
		return nil, err
	}

	if !rows.Next() {
		return nil,  fmt.Errorf("no entry found for %s %s", pkgName, channelName)
	}
	var entryId sql.NullInt64
	var outName sql.NullString
	var bundle sql.NullString
	var bundlePath sql.NullString
	if err := rows.Scan(&entryId, &outName, &bundle, &bundlePath); err != nil {
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

	provided, required, err := s.GetApisForEntry(ctx, entryId.Int64)
	if err != nil {
		return nil, err
	}
	out.ProvidedApis = provided
	out.RequiredApis = required

	return out, nil
}

func (s *SQLQuerier) GetChannelEntriesThatProvide(ctx context.Context, group, version, kind string) (entries []*registry.ChannelEntry, err error) {
	query := `SELECT DISTINCT channel_entry.package_name, channel_entry.channel_name, channel_entry.operatorbundle_name, replaces.operatorbundle_name
          FROM channel_entry
          INNER JOIN api_provider ON channel_entry.entry_id = api_provider.channel_entry_id
          LEFT OUTER JOIN channel_entry replaces ON channel_entry.replaces = replaces.entry_id
		  WHERE api_provider.group_name = ? AND api_provider.version = ? AND api_provider.kind = ?`

	rows, err := s.db.QueryContext(ctx, query, group, version, kind)
	if err != nil {
		return
	}

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
          INNER JOIN api_provider ON channel_entry.entry_id = api_provider.channel_entry_id
		  LEFT OUTER JOIN channel_entry replaces ON channel_entry.replaces = replaces.entry_id
		  WHERE api_provider.group_name = ? AND api_provider.version = ? AND api_provider.kind = ?
		  GROUP BY channel_entry.package_name, channel_entry.channel_name`
	rows, err := s.db.QueryContext(ctx, query, group, version, kind)
	if err != nil {
		return nil, err
	}

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
func (s *SQLQuerier) GetBundleThatProvides(ctx context.Context, group, version, kind string) (*api.Bundle, error) {
	query := `SELECT DISTINCT channel_entry.entry_id, operatorbundle.bundle, operatorbundle.bundlepath, MIN(channel_entry.depth), channel_entry.operatorbundle_name, channel_entry.package_name, channel_entry.channel_name, channel_entry.replaces
          FROM channel_entry
          INNER JOIN api_provider ON channel_entry.entry_id = api_provider.channel_entry_id
		  INNER JOIN operatorbundle ON operatorbundle.name = channel_entry.operatorbundle_name
		  INNER JOIN package ON package.name = channel_entry.package_name
		  WHERE api_provider.group_name = ? AND api_provider.version = ? AND api_provider.kind = ? AND package.default_channel = channel_entry.channel_name
		  GROUP BY channel_entry.package_name, channel_entry.channel_name`

	rows, err := s.db.QueryContext(ctx, query, group, version, kind)
	if err != nil {
		return nil, err
	}

	if !rows.Next() {
		return nil, fmt.Errorf("no entry found that provides %s %s %s", group, version, kind)
	}
	var entryId sql.NullInt64
	var bundle sql.NullString
	var bundlePath sql.NullString
	var min_depth sql.NullInt64
	var bundleName sql.NullString
	var pkgName sql.NullString
	var channelName sql.NullString
	var replaces sql.NullString
	if err := rows.Scan(&entryId, &bundle, &bundlePath, &min_depth, &bundleName, &pkgName, &channelName, &replaces); err != nil {
		return nil, err
	}

	if !bundle.Valid {
		return nil, fmt.Errorf("no entry found that provides %s %s %s", group, version, kind)
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

	provided, required, err := s.GetApisForEntry(ctx, entryId.Int64)
	if err != nil {
		return nil, err
	}
	out.ProvidedApis = provided
	out.RequiredApis = required

	return out, nil
}

func (s *SQLQuerier) ListImages(ctx context.Context) ([]string, error) {
	query := "SELECT DISTINCT image FROM related_image"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
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

func (s *SQLQuerier) GetApisForEntry(ctx context.Context, entryId int64) (provided []*api.GroupVersionKind, required []*api.GroupVersionKind, err error) {
	providedQuery := `SELECT DISTINCT api.group_name, api.version, api.kind, api.plural FROM api
		 	  		  INNER JOIN api_provider ON (api.group_name=api_provider.group_name AND api.version=api_provider.version AND api.kind=api_provider.kind)
			  		  WHERE api_provider.channel_entry_id=?`

	providedRows, err := s.db.QueryContext(ctx, providedQuery, entryId)
	if err != nil {
		return nil,nil, err
	}
	provided = []*api.GroupVersionKind{}
	for providedRows.Next() {
		var groupName sql.NullString
		var versionName sql.NullString
		var kindName sql.NullString
		var pluralName sql.NullString

		if err := providedRows.Scan(&groupName, &versionName, &kindName, &pluralName); err != nil {
			return nil, nil, err
		}
		if !groupName.Valid || !versionName.Valid || !kindName.Valid || !pluralName.Valid {
			return nil, nil, err
		}
		provided = append(provided, &api.GroupVersionKind{
			Group:  groupName.String,
			Version: versionName.String,
			Kind:   kindName.String,
			Plural:   pluralName.String,
		})
	}

	requiredQuery := `SELECT DISTINCT api.group_name, api.version, api.kind, api.plural FROM api
		 	  		  INNER JOIN api_requirer ON (api.group_name=api_requirer.group_name AND api.version=api_requirer.version AND api.kind=api_requirer.kind)
			  		  WHERE api_requirer.channel_entry_id=?`

	requiredRows, err := s.db.QueryContext(ctx, requiredQuery, entryId)
	if err != nil {
		return nil,nil, err
	}
	required = []*api.GroupVersionKind{}
	for requiredRows.Next() {
		var groupName sql.NullString
		var versionName sql.NullString
		var kindName sql.NullString
		var pluralName sql.NullString

		if err := requiredRows.Scan(&groupName, &versionName, &kindName, &pluralName); err != nil {
			return nil, nil, err
		}
		if !groupName.Valid || !versionName.Valid || !kindName.Valid || !pluralName.Valid {
			return nil, nil, err
		}
		required = append(required, &api.GroupVersionKind{
			Group:  groupName.String,
			Version: versionName.String,
			Kind:   kindName.String,
			Plural:   pluralName.String,
		})
	}

	return
}
