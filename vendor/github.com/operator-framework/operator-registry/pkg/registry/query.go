package registry

import (
	"context"
	"fmt"
	"sort"

	"github.com/operator-framework/operator-registry/internal/model"
	"github.com/operator-framework/operator-registry/pkg/api"
)

type Querier struct {
	pkgs model.Model
}

var _ GRPCQuery = &Querier{}

func NewQuerier(packages model.Model) *Querier {
	return &Querier{
		pkgs: packages,
	}
}

func (q Querier) ListPackages(_ context.Context) ([]string, error) {
	var packages []string
	for pkgName := range q.pkgs {
		packages = append(packages, pkgName)
	}
	return packages, nil
}

func (q Querier) ListBundles(_ context.Context) ([]*api.Bundle, error) {
	var bundles []*api.Bundle

	for _, pkg := range q.pkgs {
		for _, ch := range pkg.Channels {
			for _, b := range ch.Bundles {
				apiBundle, err := api.ConvertModelBundleToAPIBundle(*b)
				if err != nil {
					return nil, fmt.Errorf("convert bundle %q: %v", b.Name, err)
				}
				bundles = append(bundles, apiBundle)
			}
		}
	}
	return bundles, nil
}

func (q Querier) GetPackage(_ context.Context, name string) (*PackageManifest, error) {
	pkg, ok := q.pkgs[name]
	if !ok {
		return nil, fmt.Errorf("package %q not found", name)
	}

	var channels []PackageChannel
	for _, ch := range pkg.Channels {
		head, err := ch.Head()
		if err != nil {
			return nil, fmt.Errorf("package %q, channel %q has invalid head: %v", name, ch.Name, err)
		}
		channels = append(channels, PackageChannel{
			Name:           ch.Name,
			CurrentCSVName: head.Name,
		})
	}
	return &PackageManifest{
		PackageName:        pkg.Name,
		Channels:           channels,
		DefaultChannelName: pkg.DefaultChannel.Name,
	}, nil
}

func (q Querier) GetBundle(_ context.Context, pkgName, channelName, csvName string) (*api.Bundle, error) {
	pkg, ok := q.pkgs[pkgName]
	if !ok {
		return nil, fmt.Errorf("package %q not found", pkgName)
	}
	ch, ok := pkg.Channels[channelName]
	if !ok {
		return nil, fmt.Errorf("package %q, channel %q not found", pkgName, channelName)
	}
	b, ok := ch.Bundles[csvName]
	if !ok {
		return nil, fmt.Errorf("package %q, channel %q, bundle %q not found", pkgName, channelName, csvName)
	}
	apiBundle, err := api.ConvertModelBundleToAPIBundle(*b)
	if err != nil {
		return nil, fmt.Errorf("convert bundle %q: %v", b.Name, err)
	}

	// unset Replaces and Skips (sqlite query does not populate these fields)
	apiBundle.Replaces = ""
	apiBundle.Skips = nil
	return apiBundle, nil
}

func (q Querier) GetBundleForChannel(_ context.Context, pkgName string, channelName string) (*api.Bundle, error) {
	pkg, ok := q.pkgs[pkgName]
	if !ok {
		return nil, fmt.Errorf("package %q not found", pkgName)
	}
	ch, ok := pkg.Channels[channelName]
	if !ok {
		return nil, fmt.Errorf("package %q, channel %q not found", pkgName, channelName)
	}
	head, err := ch.Head()
	if err != nil {
		return nil, fmt.Errorf("package %q, channel %q has invalid head: %v", pkgName, channelName, err)
	}
	apiBundle, err := api.ConvertModelBundleToAPIBundle(*head)
	if err != nil {
		return nil, fmt.Errorf("convert bundle %q: %v", head.Name, err)
	}

	// unset Replaces and Skips (sqlite query does not populate these fields)
	apiBundle.Replaces = ""
	apiBundle.Skips = nil
	return apiBundle, nil
}

func (q Querier) GetChannelEntriesThatReplace(_ context.Context, name string) ([]*ChannelEntry, error) {
	var entries []*ChannelEntry

	for _, pkg := range q.pkgs {
		for _, ch := range pkg.Channels {
			for _, b := range ch.Bundles {
				entries = append(entries, channelEntriesThatReplace(*b, name)...)
			}
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no channel entries found that replace %s", name)
	}
	return entries, nil
}

func (q Querier) GetBundleThatReplaces(_ context.Context, name, pkgName, channelName string) (*api.Bundle, error) {
	pkg, ok := q.pkgs[pkgName]
	if !ok {
		return nil, fmt.Errorf("package %s not found", pkgName)
	}
	ch, ok := pkg.Channels[channelName]
	if !ok {
		return nil, fmt.Errorf("package %q, channel %q not found", pkgName, channelName)
	}

	// NOTE: iterating over a map is non-deterministic in Go, so if multiple bundles replace this one,
	//       the bundle returned by this function is also non-deterministic. The sqlite implementation
	//       is ALSO non-deterministic because it doesn't use ORDER BY, so its probably okay for this
	//       implementation to be non-deterministic as well.
	for _, b := range ch.Bundles {
		if bundleReplaces(*b, name) {
			apiBundle, err := api.ConvertModelBundleToAPIBundle(*b)
			if err != nil {
				return nil, fmt.Errorf("convert bundle %q: %v", b.Name, err)
			}

			// unset Replaces and Skips (sqlite query does not populate these fields)
			apiBundle.Replaces = ""
			apiBundle.Skips = nil
			return apiBundle, nil
		}
	}
	return nil, fmt.Errorf("no entry found for package %q, channel %q", pkgName, channelName)
}

func (q Querier) GetChannelEntriesThatProvide(_ context.Context, group, version, kind string) ([]*ChannelEntry, error) {
	var entries []*ChannelEntry

	for _, pkg := range q.pkgs {
		for _, ch := range pkg.Channels {
			for _, b := range ch.Bundles {
				provides, err := doesModelBundleProvide(*b, group, version, kind)
				if err != nil {
					return nil, err
				}
				if provides {
					// TODO(joelanford): It seems like the SQLite query returns
					//   invalid entries (i.e. where bundle `Replaces` isn't actually
					//   in channel `ChannelName`). Is that a bug? For now, this mimics
					//   the sqlite server and returns seemingly invalid channel entries.
					//      Don't worry about this. Not used anymore.

					entries = append(entries, channelEntriesForBundle(*b, true)...)
				}
			}
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no channel entries found that provide group:%q version:%q kind:%q", group, version, kind)
	}
	return entries, nil
}

// TODO(joelanford): Need to review the expected functionality of this function. I ran
//   some experiments with the sqlite version of this function and it seems to only return
//   channel heads that provide the GVK (rather than searching down the graph if parent bundles
//   don't provide the API). Based on that, this function currently looks at channel heads only.
//   ---
//   Separate, but possibly related, I noticed there are several channels in the channel entry
//   table who's minimum depth is 1. What causes 1 to be minimum depth in some cases and 0 in others?
func (q Querier) GetLatestChannelEntriesThatProvide(_ context.Context, group, version, kind string) ([]*ChannelEntry, error) {
	var entries []*ChannelEntry

	for _, pkg := range q.pkgs {
		for _, ch := range pkg.Channels {
			b, err := ch.Head()
			if err != nil {
				return nil, fmt.Errorf("package %q, channel %q has invalid head: %v", pkg.Name, ch.Name, err)
			}

			provides, err := doesModelBundleProvide(*b, group, version, kind)
			if err != nil {
				return nil, err
			}
			if provides {
				entries = append(entries, channelEntriesForBundle(*b, false)...)
			}
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no channel entries found that provide group:%q version:%q kind:%q", group, version, kind)
	}
	return entries, nil
}

func (q Querier) GetBundleThatProvides(ctx context.Context, group, version, kind string) (*api.Bundle, error) {
	latestEntries, err := q.GetLatestChannelEntriesThatProvide(ctx, group, version, kind)
	if err != nil {
		return nil, err
	}

	// It's possible for multiple packages to provide an API, but this function is forced to choose one.
	// To do that deterministically, we'll pick the the bundle based on a lexicographical sort of its
	// package name.
	sort.Slice(latestEntries, func(i, j int) bool {
		return latestEntries[i].PackageName < latestEntries[j].PackageName
	})

	for _, entry := range latestEntries {
		pkg, ok := q.pkgs[entry.PackageName]
		if !ok {
			// This should never happen because the latest entries were
			// collected based on iterating over the packages in q.pkgs.
			continue
		}
		if entry.ChannelName == pkg.DefaultChannel.Name {
			return q.GetBundle(ctx, entry.PackageName, entry.ChannelName, entry.BundleName)
		}
	}
	return nil, fmt.Errorf("no entry found that provides group:%q version:%q kind:%q", group, version, kind)
}

func doesModelBundleProvide(b model.Bundle, group, version, kind string) (bool, error) {
	apiBundle, err := api.ConvertModelBundleToAPIBundle(b)
	if err != nil {
		return false, fmt.Errorf("convert bundle %q: %v", b.Name, err)
	}
	for _, gvk := range apiBundle.ProvidedApis {
		if gvk.Group == group && gvk.Version == version && gvk.Kind == kind {
			return true, nil
		}
	}
	return false, nil
}

func bundleReplaces(b model.Bundle, name string) bool {
	if b.Replaces == name {
		return true
	}
	for _, s := range b.Skips {
		if s == name {
			return true
		}
	}
	return false
}

func channelEntriesThatReplace(b model.Bundle, name string) []*ChannelEntry {
	var entries []*ChannelEntry
	if b.Replaces == name {
		entries = append(entries, &ChannelEntry{
			PackageName: b.Package.Name,
			ChannelName: b.Channel.Name,
			BundleName:  b.Name,
			Replaces:    b.Replaces,
		})
	}
	for _, s := range b.Skips {
		if s == name && s != b.Replaces {
			entries = append(entries, &ChannelEntry{
				PackageName: b.Package.Name,
				ChannelName: b.Channel.Name,
				BundleName:  b.Name,
				Replaces:    b.Replaces,
			})
		}
	}
	return entries
}

func channelEntriesForBundle(b model.Bundle, ignoreChannel bool) []*ChannelEntry {
	entries := []*ChannelEntry{{
		PackageName: b.Package.Name,
		ChannelName: b.Channel.Name,
		BundleName:  b.Name,
		Replaces:    b.Replaces,
	}}
	for _, s := range b.Skips {
		// Ignore skips that duplicate b.Replaces. Also, only add it if its
		// in the same channel as b (or we're ignoring channel presence).
		if _, inChannel := b.Channel.Bundles[s]; s != b.Replaces && (ignoreChannel || inChannel) {
			entries = append(entries, &ChannelEntry{
				PackageName: b.Package.Name,
				ChannelName: b.Channel.Name,
				BundleName:  b.Name,
				Replaces:    s,
			})
		}
	}
	return entries
}
