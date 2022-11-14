package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/alpha/model"
	"github.com/operator-framework/operator-registry/pkg/api"
)

const (
	cachePermissionDir  = 0750
	cachePermissionFile = 0640
)

type Querier struct {
	*cache
}

func (q Querier) Close() error {
	return q.cache.close()
}

type apiBundleKey struct {
	pkgName string
	chName  string
	name    string
}

type SliceBundleSender []*api.Bundle

func (s *SliceBundleSender) Send(b *api.Bundle) error {

	*s = append(*s, b)
	return nil
}

var _ GRPCQuery = &Querier{}

func NewQuerierFromFS(fbcFS fs.FS, cacheDir string) (*Querier, error) {
	q := &Querier{}
	var err error
	q.cache, err = newCache(cacheDir, &fbcCacheModel{
		FBC:   fbcFS,
		Cache: os.DirFS(cacheDir),
	})
	if err != nil {
		return q, err
	}
	return q, nil
}

func NewQuerier(m model.Model) (*Querier, error) {
	q := &Querier{}
	var err error
	q.cache, err = newCache("", &nonDigestableModel{Model: m})
	if err != nil {
		return q, err
	}
	return q, nil
}

func (q Querier) loadAPIBundle(k apiBundleKey) (*api.Bundle, error) {
	filename, ok := q.apiBundles[k]
	if !ok {
		return nil, fmt.Errorf("package %q, channel %q, bundle %q not found", k.pkgName, k.chName, k.name)
	}
	d, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var b api.Bundle
	if err := json.Unmarshal(d, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (q Querier) ListPackages(_ context.Context) ([]string, error) {
	var packages []string
	for pkgName := range q.pkgs {
		packages = append(packages, pkgName)
	}
	return packages, nil
}

func (q Querier) ListBundles(ctx context.Context) ([]*api.Bundle, error) {
	var bundleSender SliceBundleSender

	err := q.SendBundles(ctx, &bundleSender)
	if err != nil {
		return nil, err
	}

	return bundleSender, nil
}

func (q Querier) SendBundles(_ context.Context, s BundleSender) error {
	for _, pkg := range q.pkgs {
		for _, ch := range pkg.Channels {
			for _, b := range ch.Bundles {
				apiBundle, err := q.loadAPIBundle(apiBundleKey{pkg.Name, ch.Name, b.Name})
				if err != nil {
					return fmt.Errorf("convert bundle %q: %v", b.Name, err)
				}
				if apiBundle.BundlePath != "" {
					// The SQLite-based server
					// configures its querier to
					// omit these fields when
					// bundle path is set.
					apiBundle.CsvJson = ""
					apiBundle.Object = nil
				}
				if err := s.Send(apiBundle); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (q Querier) GetPackage(_ context.Context, name string) (*PackageManifest, error) {
	pkg, ok := q.pkgs[name]
	if !ok {
		return nil, fmt.Errorf("package %q not found", name)
	}

	var channels []PackageChannel
	for _, ch := range pkg.Channels {
		channels = append(channels, PackageChannel{
			Name:           ch.Name,
			CurrentCSVName: ch.Head,
		})
	}
	return &PackageManifest{
		PackageName:        pkg.Name,
		Channels:           channels,
		DefaultChannelName: pkg.DefaultChannel,
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
	apiBundle, err := q.loadAPIBundle(apiBundleKey{pkg.Name, ch.Name, b.Name})
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
	apiBundle, err := q.loadAPIBundle(apiBundleKey{pkg.Name, ch.Name, ch.Head})
	if err != nil {
		return nil, fmt.Errorf("convert bundle %q: %v", ch.Head, err)
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
				entries = append(entries, channelEntriesThatReplace(b, name)...)
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
		if bundleReplaces(b, name) {
			apiBundle, err := q.loadAPIBundle(apiBundleKey{pkg.Name, ch.Name, b.Name})
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
				provides, err := q.doesModelBundleProvide(b, group, version, kind)
				if err != nil {
					return nil, err
				}
				if provides {
					// TODO(joelanford): It seems like the SQLite query returns
					//   invalid entries (i.e. where bundle `Replaces` isn't actually
					//   in channel `ChannelName`). Is that a bug? For now, this mimics
					//   the sqlite server and returns seemingly invalid channel entries.
					//      Don't worry about this. Not used anymore.

					entries = append(entries, q.channelEntriesForBundle(b, true)...)
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
			b := ch.Bundles[ch.Head]
			provides, err := q.doesModelBundleProvide(b, group, version, kind)
			if err != nil {
				return nil, err
			}
			if provides {
				entries = append(entries, q.channelEntriesForBundle(b, false)...)
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
		if entry.ChannelName == pkg.DefaultChannel {
			return q.GetBundle(ctx, entry.PackageName, entry.ChannelName, entry.BundleName)
		}
	}
	return nil, fmt.Errorf("no entry found that provides group:%q version:%q kind:%q", group, version, kind)
}

func (q Querier) doesModelBundleProvide(b cBundle, group, version, kind string) (bool, error) {
	apiBundle, err := q.loadAPIBundle(apiBundleKey{b.Package, b.Channel, b.Name})
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

func channelEntriesThatReplace(b cBundle, name string) []*ChannelEntry {
	var entries []*ChannelEntry
	if b.Replaces == name {
		entries = append(entries, &ChannelEntry{
			PackageName: b.Package,
			ChannelName: b.Channel,
			BundleName:  b.Name,
			Replaces:    b.Replaces,
		})
	}
	for _, s := range b.Skips {
		if s == name && s != b.Replaces {
			entries = append(entries, &ChannelEntry{
				PackageName: b.Package,
				ChannelName: b.Channel,
				BundleName:  b.Name,
				Replaces:    b.Replaces,
			})
		}
	}
	return entries
}

func (q Querier) channelEntriesForBundle(b cBundle, ignoreChannel bool) []*ChannelEntry {
	entries := []*ChannelEntry{{
		PackageName: b.Package,
		ChannelName: b.Channel,
		BundleName:  b.Name,
		Replaces:    b.Replaces,
	}}
	for _, s := range b.Skips {
		// Ignore skips that duplicate b.Replaces. Also, only add it if its
		// in the same channel as b (or we're ignoring channel presence).
		if _, inChannel := q.pkgs[b.Package].Channels[b.Channel].Bundles[s]; s != b.Replaces && (ignoreChannel || inChannel) {
			entries = append(entries, &ChannelEntry{
				PackageName: b.Package,
				ChannelName: b.Channel,
				BundleName:  b.Name,
				Replaces:    s,
			})
		}
	}
	return entries
}

type cache struct {
	digest     string
	baseDir    string
	persist    bool
	pkgs       map[string]cPkg
	apiBundles map[apiBundleKey]string
}

func newCache(baseDir string, model digestableModel) (*cache, error) {
	var (
		qc  *cache
		err error
	)
	if baseDir == "" {
		qc, err = newEphemeralCache()
	} else {
		qc, err = newPersistentCache(baseDir)
	}
	if err != nil {
		return nil, err
	}
	return qc, qc.load(model)
}

func (qc cache) close() error {
	if qc.persist {
		return nil
	}
	return os.RemoveAll(qc.baseDir)
}

func newEphemeralCache() (*cache, error) {
	baseDir, err := os.MkdirTemp("", "opm-serve-cache-")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "cache"), cachePermissionDir); err != nil {
		return nil, err
	}
	return &cache{
		digest:  "",
		baseDir: baseDir,
		persist: false,
	}, nil
}

func newPersistentCache(baseDir string) (*cache, error) {
	if err := os.MkdirAll(baseDir, cachePermissionDir); err != nil {
		return nil, err
	}
	qc := &cache{baseDir: baseDir, persist: true}
	if digest, err := os.ReadFile(filepath.Join(baseDir, "digest")); err == nil {
		qc.digest = strings.TrimSpace(string(digest))
	}
	return qc, nil
}

func (qc *cache) load(model digestableModel) error {
	computedDigest, err := model.GetDigest()
	if err != nil && !errors.Is(err, errNonDigestable) {
		return fmt.Errorf("compute digest: %v", err)
	}
	if err == nil && computedDigest == qc.digest {
		err = qc.loadFromCache()
		if err == nil {
			return nil
		}
		// if there _was_ an error loading from the cache,
		// we'll drop down and repopulate from scratch.
	}
	return qc.repopulateCache(model)
}

func (qc *cache) loadFromCache() error {
	packagesData, err := os.ReadFile(filepath.Join(qc.baseDir, "cache", "packages.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(packagesData, &qc.pkgs); err != nil {
		return err
	}
	qc.apiBundles = map[apiBundleKey]string{}
	for _, p := range qc.pkgs {
		for _, ch := range p.Channels {
			for _, b := range ch.Bundles {
				filename := filepath.Join(qc.baseDir, "cache", fmt.Sprintf("%s_%s_%s.json", p.Name, ch.Name, b.Name))
				qc.apiBundles[apiBundleKey{pkgName: p.Name, chName: ch.Name, name: b.Name}] = filename
			}
		}
	}
	return nil
}

func (qc *cache) repopulateCache(model digestableModel) error {
	// ensure that generated cache is available to all future users
	oldUmask := umask(000)
	defer umask(oldUmask)

	m, err := model.GetModel()
	if err != nil {
		return err
	}
	cacheDirEntries, err := os.ReadDir(qc.baseDir)
	if err != nil {
		return err
	}
	for _, e := range cacheDirEntries {
		if err := os.RemoveAll(filepath.Join(qc.baseDir, e.Name())); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(qc.baseDir, "cache"), cachePermissionDir); err != nil {
		return err
	}

	qc.pkgs, err = packagesFromModel(m)
	if err != nil {
		return err
	}

	packageJson, err := json.Marshal(qc.pkgs)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(qc.baseDir, "cache", "packages.json"), packageJson, cachePermissionFile); err != nil {
		return err
	}

	qc.apiBundles = map[apiBundleKey]string{}
	for _, p := range m {
		for _, ch := range p.Channels {
			for _, b := range ch.Bundles {
				apiBundle, err := api.ConvertModelBundleToAPIBundle(*b)
				if err != nil {
					return err
				}
				jsonBundle, err := json.Marshal(apiBundle)
				if err != nil {
					return err
				}
				filename := filepath.Join(qc.baseDir, "cache", fmt.Sprintf("%s_%s_%s.json", p.Name, ch.Name, b.Name))
				if err := os.WriteFile(filename, jsonBundle, cachePermissionFile); err != nil {
					return err
				}
				qc.apiBundles[apiBundleKey{p.Name, ch.Name, b.Name}] = filename
			}
		}
	}
	computedHash, err := model.GetDigest()
	if err == nil {
		if err := os.WriteFile(filepath.Join(qc.baseDir, "digest"), []byte(computedHash), cachePermissionFile); err != nil {
			return err
		}
	} else if !errors.Is(err, errNonDigestable) {
		return fmt.Errorf("compute digest: %v", err)
	}
	return nil
}

func packagesFromModel(m model.Model) (map[string]cPkg, error) {
	pkgs := map[string]cPkg{}
	for _, p := range m {
		newP := cPkg{
			Name:           p.Name,
			Description:    p.Description,
			DefaultChannel: p.DefaultChannel.Name,
			Channels:       map[string]cChannel{},
		}
		if p.Icon != nil {
			newP.Icon = &declcfg.Icon{
				Data:      p.Icon.Data,
				MediaType: p.Icon.MediaType,
			}
		}
		for _, ch := range p.Channels {
			head, err := ch.Head()
			if err != nil {
				return nil, err
			}
			newCh := cChannel{
				Name:    ch.Name,
				Head:    head.Name,
				Bundles: map[string]cBundle{},
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

type cPkg struct {
	Name           string        `json:"name"`
	Description    string        `json:"description"`
	Icon           *declcfg.Icon `json:"icon"`
	DefaultChannel string        `json:"defaultChannel"`
	Channels       map[string]cChannel
}

type cChannel struct {
	Name    string
	Head    string
	Bundles map[string]cBundle
}

type cBundle struct {
	Package  string   `json:"package"`
	Channel  string   `json:"channel"`
	Name     string   `json:"name"`
	Replaces string   `json:"replaces"`
	Skips    []string `json:"skips"`
}

type digestableModel interface {
	GetModel() (model.Model, error)
	GetDigest() (string, error)
}

type fbcCacheModel struct {
	FBC   fs.FS
	Cache fs.FS
}

func (m *fbcCacheModel) GetModel() (model.Model, error) {
	fbc, err := declcfg.LoadFS(m.FBC)
	if err != nil {
		return nil, err
	}
	return declcfg.ConvertToModel(*fbc)
}

func (m *fbcCacheModel) GetDigest() (string, error) {
	computedHasher := fnv.New64a()
	if err := fsToTar(computedHasher, m.FBC); err != nil {
		return "", err
	}
	if cacheFS, err := fs.Sub(m.Cache, "cache"); err == nil {
		if err := fsToTar(computedHasher, cacheFS); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("compute hash: %v", err)
		}
	}
	return fmt.Sprintf("%x", computedHasher.Sum(nil)), nil
}

var errNonDigestable = errors.New("cannot generate digest")

type nonDigestableModel struct {
	model.Model
}

func (m *nonDigestableModel) GetModel() (model.Model, error) {
	return m.Model, nil
}

func (m *nonDigestableModel) GetDigest() (string, error) {
	return "", errNonDigestable
}
