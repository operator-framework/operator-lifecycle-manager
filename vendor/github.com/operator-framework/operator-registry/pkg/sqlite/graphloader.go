package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

type SQLGraphLoader struct {
	Querier registry.Query
}

func NewSQLGraphLoader(dbFilename string) (*SQLGraphLoader, error) {
	querier, err := NewSQLLiteQuerier(dbFilename)
	if err != nil {
		return nil, err
	}

	return &SQLGraphLoader{
		Querier: querier,
	}, nil
}

func NewSQLGraphLoaderFromDB(db *sql.DB) (*SQLGraphLoader, error) {
	return &SQLGraphLoader{
		Querier: NewSQLLiteQuerierFromDb(db),
	}, nil
}

func (g *SQLGraphLoader) Generate(packageName string) (*registry.Package, error) {
	graph := &registry.Package{
		Name:     packageName,
		Channels: make(map[string]registry.Channel, 0),
	}

	ctx := context.TODO()
	defaultChannel, err := g.Querier.GetDefaultPackage(ctx, packageName)
	if err != nil {
		return graph, registry.ErrPackageNotInDatabase
	}
	graph.DefaultChannel = defaultChannel

	channelEntries, err := g.Querier.GetChannelEntriesFromPackage(ctx, packageName)
	if err != nil {
		return graph, err
	}

	existingBundles, err := g.Querier.GetBundlesForPackage(ctx, packageName)
	if err != nil {
		return graph, err
	}

	channels, err := graphFromEntries(channelEntries, existingBundles)
	if err != nil {
		return graph, err
	}
	graph.Channels = channels

	return graph, nil
}

// graphFromEntries builds the graph from a set of channel entries
func graphFromEntries(channelEntries []registry.ChannelEntryAnnotated, existingBundles map[registry.BundleKey]struct{}) (map[string]registry.Channel, error) {
	channels := map[string]registry.Channel{}

	type replaces map[registry.BundleKey]map[registry.BundleKey]struct{}

	channelGraph := map[string]replaces{}
	channelHeadCandidates := map[string]map[registry.BundleKey]struct{}{}

	// add all channels and nodes to the graph
	for _, entry := range channelEntries {
		// create channel if we haven't seen it yet
		if _, ok := channelGraph[entry.ChannelName]; !ok {
			channelGraph[entry.ChannelName] = replaces{}
		}

		key := registry.BundleKey{
			BundlePath: entry.BundlePath,
			Version:    entry.Version,
			CsvName:    entry.BundleName,
		}

		// skip synthetic channelentries that aren't pointers to actual bundles
		if _, ok := existingBundles[key]; !ok {
			continue
		}

		channelGraph[entry.ChannelName][key] = map[registry.BundleKey]struct{}{}

		// every bundle in a channel is a potential head of that channel
		if _, ok := channelHeadCandidates[entry.ChannelName]; !ok {
			channelHeadCandidates[entry.ChannelName] = map[registry.BundleKey]struct{}{key: {}}
		} else {
			channelHeadCandidates[entry.ChannelName][key] = struct{}{}
		}
	}

	for _, entry := range channelEntries {
		key := registry.BundleKey{
			BundlePath: entry.BundlePath,
			Version:    entry.Version,
			CsvName:    entry.BundleName,
		}
		replacesKey := registry.BundleKey{
			BundlePath: entry.ReplacesBundlePath,
			Version:    entry.ReplacesVersion,
			CsvName:    entry.Replaces,
		}

		if !replacesKey.IsEmpty() {
			channelGraph[entry.ChannelName][key][replacesKey] = struct{}{}
		}

		delete(channelHeadCandidates[entry.ChannelName], replacesKey)
	}

	for channelName, candidates := range channelHeadCandidates {
		if len(candidates) == 0 {
			return nil, fmt.Errorf("no channel head found for %s", channelName)
		}
		if len(candidates) > 1 {
			return nil, fmt.Errorf("multiple candidate channel heads found for %s: %v", channelName, candidates)
		}

		for head := range candidates {
			channel := registry.Channel{
				Head:  head,
				Nodes: channelGraph[channelName],
			}
			channels[channelName] = channel
		}
	}

	return channels, nil
}
