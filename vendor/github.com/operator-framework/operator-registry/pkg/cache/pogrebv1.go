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
	"sort"

	"github.com/akrylysov/pogreb"
	pogrebfs "github.com/akrylysov/pogreb/fs"
	"github.com/golang/protobuf/proto"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

var _ backend = &pogrebV1Backend{}

func newPogrebV1Backend(baseDir string) *pogrebV1Backend {
	return &pogrebV1Backend{
		baseDir: baseDir,
		bundles: newBundleKeys(),
	}
}

const (
	pogrebV1CacheModeDir  = 0770
	pogrebV1CacheModeFile = 0660

	pograbV1CacheDir = "pogreb.v1"
	pogrebDigestFile = pograbV1CacheDir + "/digest"
	pogrebDbDir      = pograbV1CacheDir + "/db"
)

type pogrebV1Backend struct {
	baseDir string
	db      *pogreb.DB
	bundles bundleKeys
}

func (q *pogrebV1Backend) Name() string {
	return pograbV1CacheDir
}

func (q *pogrebV1Backend) IsCachePresent() bool {
	entries, err := os.ReadDir(q.baseDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == pograbV1CacheDir {
			return true
		}
	}
	return false
}

func (q *pogrebV1Backend) Init() error {
	if err := q.Close(); err != nil {
		return fmt.Errorf("failed to close existing DB: %v", err)
	}
	if err := ensureEmptyDir(filepath.Join(q.baseDir, pograbV1CacheDir), pogrebV1CacheModeDir); err != nil {
		return fmt.Errorf("ensure empty cache directory: %v", err)
	}
	q.bundles = newBundleKeys()
	return q.Open()
}

func (q *pogrebV1Backend) Open() error {
	db, err := pogreb.Open(filepath.Join(q.baseDir, pogrebDbDir), &pogreb.Options{FileSystem: pogrebfs.OSMMap})
	if err != nil {
		return err
	}
	q.db = db
	return nil
}

func (q *pogrebV1Backend) Close() error {
	if q.db == nil {
		return nil
	}
	if err := q.db.Close(); err != nil {
		return err
	}

	// Recursively fixup permissions on the DB directory.
	return filepath.Walk(filepath.Join(q.baseDir, pogrebDbDir), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		switch info.Mode().Type() {
		case os.ModeDir:
			return os.Chmod(path, pogrebV1CacheModeDir)
		case 0:
			return os.Chmod(path, pogrebV1CacheModeFile)
		default:
			return nil
		}
	})
}

func (q *pogrebV1Backend) GetPackageIndex(_ context.Context) (packageIndex, error) {
	packagesData, err := q.db.Get([]byte("packages.json"))
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

func (q *pogrebV1Backend) PutPackageIndex(_ context.Context, index packageIndex) error {
	packageJson, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return q.db.Put([]byte("packages.json"), packageJson)
}

func (q *pogrebV1Backend) dbKey(in bundleKey) []byte {
	return []byte(fmt.Sprintf("bundles/%s/%s/%s", in.PackageName, in.ChannelName, in.Name))
}

func (q *pogrebV1Backend) GetBundle(_ context.Context, key bundleKey) (*api.Bundle, error) {
	d, err := q.db.Get(q.dbKey(key))
	if err != nil {
		return nil, err
	}
	var b api.Bundle
	if err := proto.Unmarshal(d, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (q *pogrebV1Backend) PutBundle(_ context.Context, key bundleKey, bundle *api.Bundle) error {
	d, err := proto.Marshal(bundle)
	if err != nil {
		return err
	}
	if err := q.db.Put(q.dbKey(key), d); err != nil {
		return err
	}
	q.bundles.Set(key)
	return nil
}

func (q *pogrebV1Backend) GetDigest(_ context.Context) (string, error) {
	return readDigestFile(filepath.Join(q.baseDir, pogrebDigestFile))
}

func (q *pogrebV1Backend) orderedKeys() ([]string, error) {
	it := q.db.Items()
	keys := make([]string, 0, q.db.Count())
	for {
		k, _, err := it.Next()
		if errors.Is(err, pogreb.ErrIterationDone) {
			break
		}
		if err != nil {
			return nil, err
		}
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	return keys, nil
}

func (q *pogrebV1Backend) writeKeyValue(w io.Writer, k []byte) error {
	v, err := q.db.Get(k)
	if err != nil {
		return err
	}
	if _, err := w.Write(k); err != nil {
		return err
	}
	if _, err := w.Write(v); err != nil {
		return err
	}
	return nil
}

func (q *pogrebV1Backend) ComputeDigest(ctx context.Context, fbcFsys fs.FS) (string, error) {
	computedHasher := fnv.New64a()

	// Use concurrency=1 to ensure deterministic ordering of meta blobs.
	loadOpts := []declcfg.LoadOption{declcfg.WithConcurrency(1)}
	if err := declcfg.WalkMetasFS(ctx, fbcFsys, func(path string, meta *declcfg.Meta, err error) error {
		if err != nil {
			return err
		}
		if _, err := computedHasher.Write(meta.Blob); err != nil {
			return err
		}
		return nil
	}, loadOpts...); err != nil {
		return "", err
	}

	orderedKeys, err := q.orderedKeys()
	if err != nil {
		return "", err
	}
	for _, dbKey := range orderedKeys {
		if err := q.writeKeyValue(computedHasher, []byte(dbKey)); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", computedHasher.Sum(nil)), nil
}

func (q *pogrebV1Backend) PutDigest(_ context.Context, digest string) error {
	return writeDigestFile(filepath.Join(q.baseDir, pogrebDigestFile), digest, pogrebV1CacheModeFile)
}

func (q *pogrebV1Backend) SendBundles(_ context.Context, s registry.BundleSender) error {
	return q.bundles.Walk(func(key bundleKey) error {
		bundleData, err := q.db.Get(q.dbKey(key))
		if err != nil {
			return fmt.Errorf("failed to get data for package %q, channel %q, key %q: %w", key.PackageName, key.ChannelName, key.Name, err)
		}
		var bundle api.Bundle
		if err := proto.Unmarshal(bundleData, &bundle); err != nil {
			return fmt.Errorf("failed to decode data for package %q, channel %q, key %q: %w", key.PackageName, key.ChannelName, key.Name, err)
		}
		return s.Send(&bundle)
	})
}
