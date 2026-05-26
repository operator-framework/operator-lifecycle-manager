package cache

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/akrylysov/pogreb"
	pogrebfs "github.com/akrylysov/pogreb/fs"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

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
	FormatPogrebV1 = "pogreb.v1"

	pogrebV1CacheModeDir  = 0770
	pogrebV1CacheModeFile = 0660

	pograbV1CacheDir = FormatPogrebV1
	pogrebDigestFile = pograbV1CacheDir + "/digest"
	pogrebDBDir      = pograbV1CacheDir + "/db"
)

type pogrebV1Backend struct {
	baseDir string
	db      *pogreb.DB
	bundles bundleKeys
}

func (q *pogrebV1Backend) Name() string {
	return FormatPogrebV1
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
	db, err := pogreb.Open(filepath.Join(q.baseDir, pogrebDBDir), &pogreb.Options{FileSystem: pogrebfs.OSMMap})
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
	return filepath.Walk(filepath.Join(q.baseDir, pogrebDBDir), func(path string, info os.FileInfo, err error) error {
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

const metaKeyPrefix = "metas/"

func (q *pogrebV1Backend) PutPackageIndex(_ context.Context, index packageIndex) error {
	packageJSON, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return q.db.Put([]byte("packages.json"), packageJSON)
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

func (q *pogrebV1Backend) metaDBKey(in metaKey) []byte {
	return []byte(fmt.Sprintf("%s%s/%s", metaKeyPrefix, in.Schema, in.PackageName))
}

func (q *pogrebV1Backend) PutMeta(_ context.Context, key metaKey, blob []byte) error {
	st := &structpb.Struct{}
	if err := st.UnmarshalJSON(blob); err != nil {
		return fmt.Errorf("parse meta JSON: %w", err)
	}
	protoBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal meta to proto: %w", err)
	}
	if len(protoBytes) > math.MaxUint32 {
		return fmt.Errorf("meta blob too large: %d bytes exceeds uint32 max", len(protoBytes))
	}
	dbKey := q.metaDBKey(key)
	existing, err := q.db.Get(dbKey)
	if err != nil {
		return fmt.Errorf("read existing meta blobs: %w", err)
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(protoBytes))) //#nosec G115 -- bounds checked above
	return q.db.Put(dbKey, append(existing, append(header, protoBytes...)...))
}

func (q *pogrebV1Backend) SendMetas(ctx context.Context, key metaKey, sender func(*structpb.Struct) error) error {
	data, err := q.db.Get(q.metaDBKey(key))
	if err != nil {
		return fmt.Errorf("read meta blobs: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	for len(data) >= 4 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		blobLen := binary.BigEndian.Uint32(data[:4])
		data = data[4:]
		if len(data) < int(blobLen) {
			return fmt.Errorf("truncated meta blob in pogreb value")
		}
		st := &structpb.Struct{}
		if err := proto.Unmarshal(data[:blobLen], st); err != nil {
			return fmt.Errorf("unmarshal meta proto: %w", err)
		}
		if err := sender(st); err != nil {
			return err
		}
		data = data[blobLen:]
	}
	if len(data) > 0 {
		return fmt.Errorf("corrupt meta blob: %d trailing bytes", len(data))
	}
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
