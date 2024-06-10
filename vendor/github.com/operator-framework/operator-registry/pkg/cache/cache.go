package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/lib/log"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

type Cache interface {
	registry.GRPCQuery

	CheckIntegrity(ctx context.Context, fbc fs.FS) error
	Build(ctx context.Context, fbc fs.FS) error
	Load(ctc context.Context) error
	Close() error
}

type backend interface {
	Name() string
	IsCachePresent() bool

	Init() error
	Open() error
	Close() error

	GetPackageIndex(context.Context) (packageIndex, error)
	PutPackageIndex(context.Context, packageIndex) error

	SendBundles(context.Context, registry.BundleSender) error
	GetBundle(context.Context, bundleKey) (*api.Bundle, error)
	PutBundle(context.Context, bundleKey, *api.Bundle) error

	GetDigest(context.Context) (string, error)
	ComputeDigest(context.Context, fs.FS) (string, error)
	PutDigest(context.Context, string) error
}

type CacheOptions struct {
	Log *logrus.Entry
}

func WithLog(log *logrus.Entry) CacheOption {
	return func(o *CacheOptions) {
		o.Log = log
	}
}

type CacheOption func(*CacheOptions)

// New creates a new Cache. It chooses a cache implementation based
// on the files it finds in the cache directory, with a preference for the
// latest iteration of the cache implementation. If the cache directory
// is non-empty and a supported cache format is not found, an error is returned.
func New(cacheDir string, cacheOpts ...CacheOption) (Cache, error) {
	opts := &CacheOptions{
		Log: log.Null(),
	}
	for _, opt := range cacheOpts {
		opt(opts)
	}
	cacheBackend, err := getDefaultBackend(cacheDir, opts.Log)
	if err != nil {
		return nil, err
	}

	if err := cacheBackend.Open(); err != nil {
		return nil, fmt.Errorf("open cache: %v", err)
	}
	return &cache{backend: cacheBackend, log: opts.Log}, nil
}

func getDefaultBackend(cacheDir string, log *logrus.Entry) (backend, error) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("detect cache format: read cache directory: %v", err)
	}

	backends := []backend{
		newPogrebV1Backend(cacheDir),
		newJSONBackend(cacheDir),
	}

	if len(entries) == 0 {
		log.WithField("backend", backends[0].Name()).Info("cache directory is empty, using preferred backend")
		return backends[0], nil
	}

	for _, backend := range backends {
		if backend.IsCachePresent() {
			log.WithField("backend", backend.Name()).Info("found existing cache contents")
			return backend, nil
		}
	}

	// Anything else is unexpected.
	entryNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == "." {
			continue
		}
		entryNames = append(entryNames, entry.Name())
	}
	return nil, fmt.Errorf("cache directory has unexpected contents: %v", strings.Join(entryNames, ","))
}

func LoadOrRebuild(ctx context.Context, c Cache, fbc fs.FS) error {
	if err := c.CheckIntegrity(ctx, fbc); err != nil {
		if err := c.Build(ctx, fbc); err != nil {
			return fmt.Errorf("failed to rebuild cache: %v", err)
		}
	}
	return c.Load(ctx)
}

var _ Cache = &cache{}

type cache struct {
	backend backend
	log     *logrus.Entry
	packageIndex
}

type bundleStreamTransformer func(*api.Bundle)
type transformingBundleSender struct {
	stream      registry.BundleSender
	transformer bundleStreamTransformer
}

func (t *transformingBundleSender) Send(b *api.Bundle) error {
	t.transformer(b)
	return t.stream.Send(b)
}

type sliceBundleSender []*api.Bundle

func (s *sliceBundleSender) Send(b *api.Bundle) error {
	*s = append(*s, b)
	return nil
}

func (c *cache) SendBundles(ctx context.Context, stream registry.BundleSender) error {
	transform := func(bundle *api.Bundle) {
		if bundle.BundlePath != "" {
			// The SQLite-based server
			// configures its querier to
			// omit these fields when
			// key path is set.
			bundle.CsvJson = ""
			bundle.Object = nil
		}
	}
	return c.backend.SendBundles(ctx, &transformingBundleSender{stream, transform})
}

func (c *cache) ListBundles(ctx context.Context) ([]*api.Bundle, error) {
	var bundleSender sliceBundleSender
	if err := c.SendBundles(ctx, &bundleSender); err != nil {
		return nil, err
	}
	return bundleSender, nil
}

func (c *cache) getTrimmedBundle(ctx context.Context, key bundleKey) (*api.Bundle, error) {
	apiBundle, err := c.backend.GetBundle(ctx, key)
	if err != nil {
		return nil, err
	}
	apiBundle.Replaces = ""
	apiBundle.Skips = nil
	return apiBundle, nil
}

func (c *cache) GetBundle(ctx context.Context, pkgName, channelName, csvName string) (*api.Bundle, error) {
	pkg, ok := c.packageIndex[pkgName]
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
	return c.getTrimmedBundle(ctx, bundleKey{pkg.Name, ch.Name, b.Name})
}

func (c *cache) GetBundleForChannel(ctx context.Context, pkgName string, channelName string) (*api.Bundle, error) {
	return c.packageIndex.GetBundleForChannel(ctx, c.getTrimmedBundle, pkgName, channelName)
}

func (c *cache) GetBundleThatReplaces(ctx context.Context, name, pkgName, channelName string) (*api.Bundle, error) {
	return c.packageIndex.GetBundleThatReplaces(ctx, c.getTrimmedBundle, name, pkgName, channelName)
}

func (c *cache) GetChannelEntriesThatProvide(ctx context.Context, group, version, kind string) ([]*registry.ChannelEntry, error) {
	return c.packageIndex.GetChannelEntriesThatProvide(ctx, c.backend.GetBundle, group, version, kind)
}

func (c *cache) GetLatestChannelEntriesThatProvide(ctx context.Context, group, version, kind string) ([]*registry.ChannelEntry, error) {
	return c.packageIndex.GetLatestChannelEntriesThatProvide(ctx, c.backend.GetBundle, group, version, kind)
}

func (c *cache) GetBundleThatProvides(ctx context.Context, group, version, kind string) (*api.Bundle, error) {
	return c.packageIndex.GetBundleThatProvides(ctx, c, group, version, kind)
}

func (c *cache) CheckIntegrity(ctx context.Context, fbc fs.FS) error {
	existingDigest, err := c.backend.GetDigest(ctx)
	if err != nil {
		return fmt.Errorf("read existing cache digest: %v", err)
	}
	computedDigest, err := c.backend.ComputeDigest(ctx, fbc)
	if err != nil {
		return fmt.Errorf("compute digest: %v", err)
	}
	if existingDigest != computedDigest {
		c.log.WithField("existingDigest", existingDigest).WithField("computedDigest", computedDigest).Warn("cache requires rebuild")
		return fmt.Errorf("cache requires rebuild: cache reports digest as %q, but computed digest is %q", existingDigest, computedDigest)
	}
	return nil
}

func (c *cache) Build(ctx context.Context, fbcFsys fs.FS) error {
	// ensure that generated cache is available to all future users
	oldUmask := umask(000)
	defer umask(oldUmask)

	c.log.Info("building cache")

	if err := c.backend.Init(); err != nil {
		return fmt.Errorf("init cache: %v", err)
	}

	tmpFile, err := os.CreateTemp("", "opm-cache-build-*.json")
	if err != nil {
		return err
	}
	defer func() {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
	}()

	var (
		concurrency      = runtime.NumCPU()
		byPackageReaders = map[string][]io.Reader{}
		walkMu           sync.Mutex
		offset           int64
	)
	if err := declcfg.WalkMetasFS(ctx, fbcFsys, func(path string, meta *declcfg.Meta, err error) error {
		if err != nil {
			return err
		}
		packageName := meta.Package
		if meta.Schema == declcfg.SchemaPackage {
			packageName = meta.Name
		}

		walkMu.Lock()
		defer walkMu.Unlock()
		if _, err := tmpFile.Write(meta.Blob); err != nil {
			return err
		}
		sr := io.NewSectionReader(tmpFile, offset, int64(len(meta.Blob)))
		byPackageReaders[packageName] = append(byPackageReaders[packageName], sr)
		offset += int64(len(meta.Blob))
		return nil
	}, declcfg.WithConcurrency(concurrency)); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return err
	}

	eg, egCtx := errgroup.WithContext(ctx)
	pkgNameChan := make(chan string, concurrency)
	eg.Go(func() error {
		defer close(pkgNameChan)
		for pkgName := range byPackageReaders {
			select {
			case <-egCtx.Done():
				return egCtx.Err()
			case pkgNameChan <- pkgName:
			}
		}
		return nil
	})

	var (
		pkgs   = packageIndex{}
		pkgsMu sync.Mutex
	)
	for i := 0; i < concurrency; i++ {
		eg.Go(func() error {
			for {
				select {
				case <-egCtx.Done():
					return egCtx.Err()
				case pkgName, ok := <-pkgNameChan:
					if !ok {
						return nil
					}
					pkgIndex, err := c.processPackage(egCtx, io.MultiReader(byPackageReaders[pkgName]...))
					if err != nil {
						return fmt.Errorf("process package %q: %v", pkgName, err)
					}

					pkgsMu.Lock()
					pkgs[pkgName] = pkgIndex[pkgName]
					pkgsMu.Unlock()
				}
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("build package index: %v", err)
	}

	if err := c.backend.PutPackageIndex(ctx, pkgs); err != nil {
		return fmt.Errorf("store package index: %v", err)
	}

	digest, err := c.backend.ComputeDigest(ctx, fbcFsys)
	if err != nil {
		return fmt.Errorf("compute digest: %v", err)
	}
	if err := c.backend.PutDigest(ctx, digest); err != nil {
		return fmt.Errorf("store digest: %v", err)
	}
	return nil
}

func (c *cache) processPackage(ctx context.Context, reader io.Reader) (packageIndex, error) {
	pkgFbc, err := declcfg.LoadReader(reader)
	if err != nil {
		return nil, err
	}
	pkgModel, err := declcfg.ConvertToModel(*pkgFbc)
	if err != nil {
		return nil, err
	}
	pkgIndex, err := packagesFromModel(pkgModel)
	if err != nil {
		return nil, err
	}
	for _, p := range pkgModel {
		for _, ch := range p.Channels {
			for _, b := range ch.Bundles {
				apiBundle, err := api.ConvertModelBundleToAPIBundle(*b)
				if err != nil {
					return nil, err
				}
				if err := c.backend.PutBundle(ctx, bundleKey{p.Name, ch.Name, b.Name}, apiBundle); err != nil {
					return nil, fmt.Errorf("store bundle %q: %v", b.Name, err)
				}
			}
		}
	}
	return pkgIndex, nil
}

func (c *cache) Load(ctx context.Context) error {
	pi, err := c.backend.GetPackageIndex(ctx)
	if err != nil {
		return fmt.Errorf("get package index: %v", err)
	}
	c.packageIndex = pi
	return nil
}

func (c *cache) Close() error {
	return c.backend.Close()
}

func ensureEmptyDir(dir string, mode os.FileMode) error {
	if err := os.MkdirAll(dir, mode); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func readDigestFile(digestFile string) (string, error) {
	existingDigestBytes, err := os.ReadFile(digestFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(existingDigestBytes)), nil
}

func writeDigestFile(file string, digest string, mode os.FileMode) error {
	return os.WriteFile(file, []byte(digest), mode)
}

func doesBundleProvide(ctx context.Context, getBundle getBundleFunc, pkgName, chName, bundleName, group, version, kind string) (bool, error) {
	apiBundle, err := getBundle(ctx, bundleKey{pkgName, chName, bundleName})
	if err != nil {
		return false, fmt.Errorf("get bundle %q: %v", bundleName, err)
	}
	for _, gvk := range apiBundle.ProvidedApis {
		if gvk.Group == group && gvk.Version == version && gvk.Kind == kind {
			return true, nil
		}
	}
	return false, nil
}
