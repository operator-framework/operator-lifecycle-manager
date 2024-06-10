package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

var _ backend = &jsonBackend{}

func newJSONBackend(baseDir string) *jsonBackend {
	return &jsonBackend{
		baseDir: baseDir,
		bundles: newBundleKeys(),
	}
}

const (
	jsonCacheModeDir  = 0750
	jsonCacheModeFile = 0640

	jsonDigestFile   = "digest"
	jsonDir          = "cache"
	jsonPackagesFile = jsonDir + string(filepath.Separator) + "packages.json"
)

type jsonBackend struct {
	baseDir string
	bundles bundleKeys
}

func (q *jsonBackend) Name() string {
	return "json"
}

func (q *jsonBackend) IsCachePresent() bool {
	entries, err := os.ReadDir(q.baseDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false
	}
	var hasDir, hasDigest bool
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == jsonDir {
			hasDir = true
		}
		if entry.Name() == jsonDigestFile {
			hasDigest = true
		}
	}
	return hasDir && hasDigest
}

func (q *jsonBackend) Init() error {
	if err := ensureEmptyDir(filepath.Join(q.baseDir, jsonDir), jsonCacheModeDir); err != nil {
		return fmt.Errorf("failed to ensure JSON cache directory: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(q.baseDir, jsonDigestFile)); err != nil {
		return fmt.Errorf("failed to remove existing JSON digest file: %v", err)
	}
	q.bundles = newBundleKeys()
	return nil
}

func (q *jsonBackend) Open() error {
	return nil
}

func (q *jsonBackend) Close() error {
	return nil
}

func (q *jsonBackend) GetPackageIndex(_ context.Context) (packageIndex, error) {
	packagesData, err := os.ReadFile(filepath.Join(q.baseDir, jsonPackagesFile))
	if err != nil {
		return nil, err
	}
	var pi packageIndex
	if err := json.Unmarshal(packagesData, &pi); err != nil {
		return nil, err
	}
	for _, pkg := range pi {
		for _, ch := range pkg.Channels {
			for _, b := range ch.Bundles {
				q.bundles.Set(bundleKey{PackageName: pkg.Name, ChannelName: ch.Name, Name: b.Name})
			}
		}
	}
	return pi, nil
}

func (q *jsonBackend) PutPackageIndex(_ context.Context, pi packageIndex) error {
	packageJson, err := json.Marshal(pi)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(q.baseDir, jsonPackagesFile), packageJson, jsonCacheModeFile); err != nil {
		return err
	}
	return nil
}

func (q *jsonBackend) bundleFile(in bundleKey) string {
	return filepath.Join(q.baseDir, jsonDir, fmt.Sprintf("%s_%s_%s.json", in.PackageName, in.ChannelName, in.Name))
}

func (q *jsonBackend) GetBundle(_ context.Context, key bundleKey) (*api.Bundle, error) {
	d, err := os.ReadFile(q.bundleFile(key))
	if err != nil {
		return nil, err
	}
	var b api.Bundle
	if err := json.Unmarshal(d, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (q *jsonBackend) PutBundle(_ context.Context, key bundleKey, bundle *api.Bundle) error {
	d, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if err := os.WriteFile(q.bundleFile(key), d, jsonCacheModeFile); err != nil {
		return err
	}
	q.bundles.Set(key)
	return nil
}

func (q *jsonBackend) GetDigest(_ context.Context) (string, error) {
	return readDigestFile(filepath.Join(q.baseDir, jsonDigestFile))
}

func (q *jsonBackend) ComputeDigest(_ context.Context, fbcFsys fs.FS) (string, error) {
	// We are not sensitive to the size of this buffer, we just need it to be shared.
	// For simplicity, do the same as io.Copy() would.
	buf := make([]byte, 32*1024)
	computedHasher := fnv.New64a()
	if err := fsToTar(computedHasher, fbcFsys, buf); err != nil {
		return "", err
	}

	if cacheFS, err := fs.Sub(os.DirFS(q.baseDir), jsonDir); err == nil {
		if err := fsToTar(computedHasher, cacheFS, buf); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("compute hash: %v", err)
		}
	}
	return fmt.Sprintf("%x", computedHasher.Sum(nil)), nil
}

func (q *jsonBackend) PutDigest(_ context.Context, digest string) error {
	return writeDigestFile(filepath.Join(q.baseDir, jsonDigestFile), digest, jsonCacheModeFile)
}

func (q *jsonBackend) SendBundles(_ context.Context, s registry.BundleSender) error {
	keys := make([]bundleKey, 0, q.bundles.Len())
	files := make([]*os.File, 0, q.bundles.Len())
	readers := make([]io.Reader, 0, q.bundles.Len())
	if err := q.bundles.Walk(func(key bundleKey) error {
		file, err := os.Open(q.bundleFile(key))
		if err != nil {
			return fmt.Errorf("failed to open file for package %q, channel %q, key %q: %w", key.PackageName, key.ChannelName, key.Name, err)
		}
		keys = append(keys, key)
		files = append(files, file)
		readers = append(readers, file)
		return nil
	}); err != nil {
		return err
	}
	defer func() {
		for _, file := range files {
			if err := file.Close(); err != nil {
				logrus.WithError(err).WithField("file", file.Name()).Warn("could not close file")
			}
		}
	}()
	multiReader := io.MultiReader(readers...)
	decoder := json.NewDecoder(multiReader)
	index := 0
	for {
		var bundle api.Bundle
		if err := decoder.Decode(&bundle); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("failed to decode file for package %q, channel %q, key %q: %w", keys[index].PackageName, keys[index].ChannelName, keys[index].Name, err)
		}
		if err := s.Send(&bundle); err != nil {
			return err
		}
		index += 1
	}
	return nil
}
