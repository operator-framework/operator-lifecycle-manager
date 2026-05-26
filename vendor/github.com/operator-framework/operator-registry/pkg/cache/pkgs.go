package cache

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/operator-framework/operator-registry/alpha/model"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

type packageIndex map[string]cPkg

func (pkgs packageIndex) ListPackages(_ context.Context) ([]string, error) {
	// nolint:prealloc
	var packages []string
	for pkgName := range pkgs {
		packages = append(packages, pkgName)
	}
	return packages, nil
}

func (pkgs packageIndex) GetPackage(_ context.Context, name string) (*registry.PackageManifest, error) {
	pkg, ok := pkgs[name]
	if !ok {
		return nil, fmt.Errorf("package %q not found", name)
	}

	// nolint:prealloc
	var channels []registry.PackageChannel
	for _, ch := range pkg.Channels {
		var deprecation *registry.Deprecation
		if ch.Deprecation != nil {
			deprecation = &registry.Deprecation{Message: ch.Deprecation.Message}
		}
		channels = append(channels, registry.PackageChannel{
			Name:           ch.Name,
			CurrentCSVName: ch.Head,
			Deprecation:    deprecation,
		})
	}
	sort.Slice(channels, func(i, j int) bool { return strings.Compare(channels[i].Name, channels[j].Name) < 0 })
	registryPackage := &registry.PackageManifest{
		PackageName:        pkg.Name,
		Channels:           channels,
		DefaultChannelName: pkg.DefaultChannel,
	}
	if pkg.Deprecation != nil {
		registryPackage.Deprecation = &registry.Deprecation{Message: pkg.Deprecation.Message}
	}
	return registryPackage, nil
}

func (pkgs packageIndex) GetChannelEntriesThatReplace(_ context.Context, name string) ([]*registry.ChannelEntry, error) {
	entries := make([]*registry.ChannelEntry, 0, len(pkgs))

	for _, pkg := range pkgs {
		for _, ch := range pkg.Channels {
			for _, b := range ch.Bundles {
				entries = append(entries, channelEntriesThatReplace(b, name)...)
			}
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no channel entries found that replace %s", name)
	}
	return entries, nil
}

type getBundleFunc func(context.Context, bundleKey) (*api.Bundle, error)

func (pkgs packageIndex) GetBundleForChannel(ctx context.Context, getBundle getBundleFunc, pkgName string, channelName string) (*api.Bundle, error) {
	pkg, ok := pkgs[pkgName]
	if !ok {
		return nil, fmt.Errorf("package %q not found", pkgName)
	}
	ch, ok := pkg.Channels[channelName]
	if !ok {
		return nil, fmt.Errorf("package %q, channel %q not found", pkgName, channelName)
	}
	return getBundle(ctx, bundleKey{pkg.Name, ch.Name, ch.Head})
}

func (pkgs packageIndex) GetBundleThatReplaces(ctx context.Context, getBundle getBundleFunc, name, pkgName, channelName string) (*api.Bundle, error) {
	pkg, ok := pkgs[pkgName]
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
		if bundleReplaces(b, name) {
			return getBundle(ctx, bundleKey{pkg.Name, ch.Name, b.Name})
		}
	}
	return nil, fmt.Errorf("no entry found for package %q, channel %q", pkgName, channelName)
}

func (pkgs packageIndex) GetChannelEntriesThatProvide(ctx context.Context, getBundle getBundleFunc, group, version, kind string) ([]*registry.ChannelEntry, error) {
	var entries []*registry.ChannelEntry

	for _, pkg := range pkgs {
		for _, ch := range pkg.Channels {
			for _, b := range ch.Bundles {
				provides, err := doesBundleProvide(ctx, getBundle, b.Package, b.Channel, b.Name, group, version, kind)
				if err != nil {
					return nil, err
				}
				if provides {
					// TODO(joelanford): It seems like the SQLite query returns
					//   invalid entries (i.e. where bundle `Replaces` isn't actually
					//   in channel `ChannelName`). Is that a bug? For now, this mimics
					//   the sqlite server and returns seemingly invalid channel entries.
					//      Don't worry about this. Not used anymore.

					entries = append(entries, pkgs.channelEntriesForBundle(b, true)...)
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
//
//	some experiments with the sqlite version of this function and it seems to only return
//	channel heads that provide the GVK (rather than searching down the graph if parent bundles
//	don't provide the API). Based on that, this function currently looks at channel heads only.
//	---
//	Separate, but possibly related, I noticed there are several channels in the channel entry
//	table who's minimum depth is 1. What causes 1 to be minimum depth in some cases and 0 in others?
func (pkgs packageIndex) GetLatestChannelEntriesThatProvide(ctx context.Context, getBundle getBundleFunc, group, version, kind string) ([]*registry.ChannelEntry, error) {
	var entries []*registry.ChannelEntry

	for _, pkg := range pkgs {
		for _, ch := range pkg.Channels {
			b := ch.Bundles[ch.Head]
			provides, err := doesBundleProvide(ctx, getBundle, b.Package, b.Channel, b.Name, group, version, kind)
			if err != nil {
				return nil, err
			}
			if provides {
				entries = append(entries, pkgs.channelEntriesForBundle(b, false)...)
			}
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no channel entries found that provide group:%q version:%q kind:%q", group, version, kind)
	}
	return entries, nil
}

func (pkgs packageIndex) GetBundleThatProvides(ctx context.Context, c Cache, group, version, kind string) (*api.Bundle, error) {
	latestEntries, err := c.GetLatestChannelEntriesThatProvide(ctx, group, version, kind)
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
		pkg, ok := pkgs[entry.PackageName]
		if !ok {
			// This should never happen because the latest entries were
			// collected based on iterating over the packages in q.packageIndex.
			continue
		}
		if entry.ChannelName == pkg.DefaultChannel {
			return c.GetBundle(ctx, entry.PackageName, entry.ChannelName, entry.BundleName)
		}
	}
	return nil, fmt.Errorf("no entry found that provides group:%q version:%q kind:%q", group, version, kind)
}

type cPkg struct {
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	Icon           *model.Icon `json:"icon"`
	DefaultChannel string      `json:"defaultChannel"`
	Channels       map[string]cChannel
	Deprecation    *model.Deprecation `json:"deprecation,omitempty"`
}

type cChannel struct {
	Name        string
	Head        string
	Bundles     map[string]cBundle
	Deprecation *model.Deprecation `json:"deprecation,omitempty"`
}

type cBundle struct {
	Package  string   `json:"package"`
	Channel  string   `json:"channel"`
	Name     string   `json:"name"`
	Replaces string   `json:"replaces"`
	Skips    []string `json:"skips"`
}

func packagesFromModel(m model.Model) (map[string]cPkg, error) {
	pkgs := map[string]cPkg{}
	for _, p := range m {
		newP := cPkg{
			Name:           p.Name,
			Icon:           p.Icon,
			Description:    p.Description,
			DefaultChannel: p.DefaultChannel.Name,
			Channels:       map[string]cChannel{},
			Deprecation:    p.Deprecation,
		}
		for _, ch := range p.Channels {
			head, err := ch.Head()
			if err != nil {
				return nil, err
			}
			newCh := cChannel{
				Name:        ch.Name,
				Head:        head.Name,
				Bundles:     map[string]cBundle{},
				Deprecation: ch.Deprecation,
			}
			for _, b := range ch.Bundles {
				newB := cBundle{
					Package:  b.Package.Name,
					Channel:  b.Channel.Name,
					Name:     b.Name,
					Replaces: b.Replaces,
					Skips:    b.Skips,
				}
				newCh.Bundles[b.Name] = newB
			}
			newP.Channels[ch.Name] = newCh
		}
		pkgs[p.Name] = newP
	}
	return pkgs, nil
}

func bundleReplaces(b cBundle, name string) bool {
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

func channelEntriesThatReplace(b cBundle, name string) []*registry.ChannelEntry {
	var entries []*registry.ChannelEntry
	if b.Replaces == name {
		entries = append(entries, &registry.ChannelEntry{
			PackageName: b.Package,
			ChannelName: b.Channel,
			BundleName:  b.Name,
			Replaces:    b.Replaces,
		})
	}
	for _, s := range b.Skips {
		if s == name && s != b.Replaces {
			entries = append(entries, &registry.ChannelEntry{
				PackageName: b.Package,
				ChannelName: b.Channel,
				BundleName:  b.Name,
				Replaces:    b.Replaces,
			})
		}
	}
	return entries
}

func (pkgs packageIndex) channelEntriesForBundle(b cBundle, ignoreChannel bool) []*registry.ChannelEntry {
	entries := []*registry.ChannelEntry{{
		PackageName: b.Package,
		ChannelName: b.Channel,
		BundleName:  b.Name,
		Replaces:    b.Replaces,
	}}
	for _, s := range b.Skips {
		// Ignore skips that duplicate b.Replaces. Also, only add it if its
		// in the same channel as b (or we're ignoring channel presence).
		if _, inChannel := pkgs[b.Package].Channels[b.Channel].Bundles[s]; s != b.Replaces && (ignoreChannel || inChannel) {
			entries = append(entries, &registry.ChannelEntry{
				PackageName: b.Package,
				ChannelName: b.Channel,
				BundleName:  b.Name,
				Replaces:    s,
			})
		}
	}
	return entries
}
