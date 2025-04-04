package containerdregistry

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	contentlocal "github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
)

type RegistryConfig struct {
	Log               *logrus.Entry
	ResolverConfigDir string
	DBPath            string
	CacheDir          string
	PreserveCache     bool
	SkipTLSVerify     bool
	PlainHTTP         bool
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
func NewRegistry(options ...RegistryOption) (*Registry, error) {
	var registry *Registry

	config := defaultConfig()
	config.apply(options)
	if err := config.complete(); err != nil {
		return nil, err
	}

	cs, err := contentlocal.NewStore(config.CacheDir)
	if err != nil {
		return nil, err
	}

	var bdb *bolt.DB
	bdb, err = bolt.Open(config.DBPath, 0644, nil)
	if err != nil {
		return nil, err
	}

	var once sync.Once
	// nolint:nonamedreturns
	destroy := func() (destroyErr error) {
		once.Do(func() {
			if err := bdb.Close(); err != nil {
				return
			}
			if config.PreserveCache {
				return
			}

			destroyErr = os.RemoveAll(config.CacheDir)
		})

		return
	}

	httpClient := newClient(config.SkipTLSVerify, config.Roots)
	registry = &Registry{
		Store:   newStore(metadata.NewDB(bdb, cs, nil)),
		destroy: destroy,
		log:     config.Log,
		resolverFunc: func(repo string) (remotes.Resolver, error) {
			return NewResolver(httpClient, config.ResolverConfigDir, config.PlainHTTP, repo)
		},
		// nolint: staticcheck
		platform: platforms.Ordered(platforms.DefaultSpec(), specs.Platform{
			OS:           "linux",
			Architecture: "amd64",
		}),
	}
	return registry, nil
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

func SkipTLSVerify(skip bool) RegistryOption {
	return func(config *RegistryConfig) {
		config.SkipTLSVerify = skip
	}
}

func WithPlainHTTP(insecure bool) RegistryOption {
	return func(config *RegistryConfig) {
		config.PlainHTTP = insecure
	}
}

func newClient(skipTlSVerify bool, roots *x509.CertPool) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
			RootCAs:            roots,
			MinVersion:         tls.VersionTLS12,
		},
	}

	if skipTlSVerify {
		transport.TLSClientConfig = &tls.Config{
			// nolint:gosec
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		}
	}
	headers := http.Header{}
	headers.Set("User-Agent", "opm/alpha")

	return &http.Client{Transport: transport}
}
