package containerdregistry

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"sync"

	contentlocal "github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
)

type RegistryConfig struct {
	Log               *logrus.Entry
	ResolverConfigDir string
	DBPath            string
	CacheDir          string
	PreserveCache     bool
	SkipTLS           bool
	Roots             *x509.CertPool
}

func (r *RegistryConfig) apply(options []RegistryOption) {
	for _, option := range options {
		option(r)
	}
}

func (r *RegistryConfig) complete() error {
	if err := os.Mkdir(r.CacheDir, os.ModePerm); err != nil && !os.IsExist(err) {
		return err
	}

	if r.DBPath == "" {
		r.DBPath = filepath.Join(r.CacheDir, "metadata.db")
	}

	return nil
}

func defaultConfig() *RegistryConfig {
	config := &RegistryConfig{
		Log:               logrus.NewEntry(logrus.New()),
		ResolverConfigDir: "",
		CacheDir:          "cache",
	}

	return config
}

// NewRegistry returns a new containerd Registry and a function to destroy it after use.
// The destroy function is safe to call more than once, but is a no-op after the first call.
func NewRegistry(options ...RegistryOption) (registry *Registry, err error) {
	config := defaultConfig()
	config.apply(options)
	if err = config.complete(); err != nil {
		return
	}

	cs, err := contentlocal.NewStore(config.CacheDir)
	if err != nil {
		return
	}

	var bdb *bolt.DB
	bdb, err = bolt.Open(config.DBPath, 0644, nil)
	if err != nil {
		return
	}

	var once sync.Once
	destroy := func() (destroyErr error) {
		once.Do(func() {
			if destroyErr = bdb.Close(); destroyErr != nil {
				return
			}
			if config.PreserveCache {
				return
			}

			destroyErr = os.RemoveAll(config.CacheDir)
		})

		return
	}

	var resolver remotes.Resolver
	resolver, err = NewResolver(config.ResolverConfigDir, config.SkipTLS, config.Roots)
	if err != nil {
		return
	}

	registry = &Registry{
		Store:    newStore(metadata.NewDB(bdb, cs, nil)),
		destroy:  destroy,
		log:      config.Log,
		resolver: resolver,
		platform: platforms.Only(platforms.DefaultSpec()),
	}
	return
}

type RegistryOption func(config *RegistryConfig)

func WithLog(log *logrus.Entry) RegistryOption {
	return func(config *RegistryConfig) {
		config.Log = log
	}
}

func WithResolverConfigDir(path string) RegistryOption {
	return func(config *RegistryConfig) {
		config.ResolverConfigDir = path
	}
}

func WithCacheDir(dir string) RegistryOption {
	return func(config *RegistryConfig) {
		config.CacheDir = dir
	}
}

func WithRootCAs(pool *x509.CertPool) RegistryOption {
	return func(config *RegistryConfig) {
		config.Roots = pool
	}
}

func PreserveCache(preserve bool) RegistryOption {
	return func(config *RegistryConfig) {
		config.PreserveCache = preserve
	}
}

func SkipTLS(skip bool) RegistryOption {
	return func(config *RegistryConfig) {
		config.SkipTLS = skip
	}
}
