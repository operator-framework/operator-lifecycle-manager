package grpc

import (
	"context"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/operator-framework/operator-registry/pkg/client"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SourceMeta struct {
	Address         string
	LastConnect     metav1.Time
	ConnectionState connectivity.State
}

type SourceState struct {
	Key   registry.CatalogKey
	State connectivity.State
}

type SourceConn struct {
	SourceMeta
	Conn   *grpc.ClientConn
	cancel context.CancelFunc
}

type SourceStore struct {
	sync.Once
	sources      map[registry.CatalogKey]SourceConn
	sourcesLock  sync.RWMutex
	syncFn       func(SourceState)
	logger       *logrus.Logger
	notify       chan SourceState
	timeout      time.Duration
	readyTimeout time.Duration
}

func NewSourceStore(logger *logrus.Logger, timeout, readyTimeout time.Duration, sync func(SourceState)) *SourceStore {
	return &SourceStore{
		sources:      make(map[registry.CatalogKey]SourceConn),
		notify:       make(chan SourceState),
		syncFn:       sync,
		logger:       logger,
		timeout:      timeout,
		readyTimeout: readyTimeout,
	}
}

func (s *SourceStore) Start(ctx context.Context) {
	s.logger.Debug("starting source manager")
	go func() {
		s.Do(func() {
			for {
				select {
				case <-ctx.Done():
					s.logger.Debug("closing source manager")
					return
				case e := <-s.notify:
					s.logger.Debugf("Got source event: %#v", e)
					s.syncFn(e)
				}
			}
		})
	}()
}

func (s *SourceStore) GetMeta(key registry.CatalogKey) *SourceMeta {
	s.sourcesLock.RLock()
	source, ok := s.sources[key]
	s.sourcesLock.RUnlock()
	if !ok {
		return nil
	}

	return &source.SourceMeta
}

func (s *SourceStore) Exists(key registry.CatalogKey) bool {
	s.sourcesLock.RLock()
	_, ok := s.sources[key]
	s.sourcesLock.RUnlock()
	return ok
}

func (s *SourceStore) Get(key registry.CatalogKey) *SourceConn {
	s.sourcesLock.RLock()
	source, ok := s.sources[key]
	s.sourcesLock.RUnlock()
	if !ok {
		return nil
	}
	return &source
}

func grpcProxyURL(addr string) (*url.URL, error) {
	// Handle ip addresses
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	url, err := url.Parse(host)
	if err != nil {
		return nil, err
	}

	// Hardcode fields required for proxy resolution
	url.Host = addr
	url.Scheme = "http"

	// Override HTTPS_PROXY and HTTP_PROXY with GRPC_PROXY
	proxyConfig := &httpproxy.Config{
		HTTPProxy:  getGRPCProxyEnv(),
		HTTPSProxy: getGRPCProxyEnv(),
		NoProxy:    getEnvAny("NO_PROXY", "no_proxy"),
		CGI:        os.Getenv("REQUEST_METHOD") != "",
	}

	// Check if a proxy should be used based on environment variables
	return proxyConfig.ProxyFunc()(url)
}

func getGRPCProxyEnv() string {
	return getEnvAny("GRPC_PROXY", "grpc_proxy")
}

func getEnvAny(names ...string) string {
	for _, n := range names {
		if val := os.Getenv(n); val != "" {
			return val
		}
	}
	return ""
}

func grpcConnection(address string) (*grpc.ClientConn, error) {
	dialOptions := []grpc.DialOption{grpc.WithInsecure()}
	proxyURL, err := grpcProxyURL(address)
	if err != nil {
		return nil, err
	}

	if proxyURL != nil {
		dialOptions = append(dialOptions, grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			dialer, err := proxy.FromURL(proxyURL, &net.Dialer{})
			if err != nil {
				return nil, err
			}
			return dialer.Dial("tcp", addr)
		}))
	}

	return grpc.Dial(address, dialOptions...)
}

func (s *SourceStore) Add(key registry.CatalogKey, address string) (*SourceConn, error) {
	_ = s.Remove(key)

	conn, err := grpcConnection(address)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	source := SourceConn{
		SourceMeta: SourceMeta{
			Address:         address,
			LastConnect:     metav1.Now(),
			ConnectionState: connectivity.Idle,
		},
		Conn:   conn,
		cancel: cancel,
	}

	s.sourcesLock.Lock()
	s.sources[key] = source
	s.sourcesLock.Unlock()

	go s.watch(ctx, key, source)

	return &source, nil
}

func (s *SourceStore) stateTimeout(state connectivity.State) time.Duration {
	if state == connectivity.Ready {
		return s.readyTimeout
	}
	return s.timeout
}

func (s *SourceStore) watch(ctx context.Context, key registry.CatalogKey, source SourceConn) {
	state := source.ConnectionState
	for {
		select {
		case <-ctx.Done():
			return
		default:
			func() {
				timer, cancel := context.WithTimeout(ctx, s.stateTimeout(state))
				defer cancel()
				if source.Conn.WaitForStateChange(timer, state) {
					newState := source.Conn.GetState()
					state = newState

					// update connection state
					src := s.Get(key)
					if src == nil {
						// source was removed, cleanup this goroutine
						return
					}

					src.LastConnect = metav1.Now()
					src.ConnectionState = newState
					s.sourcesLock.Lock()
					s.sources[key] = *src
					s.sourcesLock.Unlock()

					// notify subscriber
					s.notify <- SourceState{Key: key, State: newState}
				}
			}()
		}
	}
}

func (s *SourceStore) Remove(key registry.CatalogKey) error {
	s.sourcesLock.RLock()
	source, ok := s.sources[key]
	s.sourcesLock.RUnlock()

	// no source to close
	if !ok {
		return nil
	}

	s.sourcesLock.Lock()
	delete(s.sources, key)
	s.sourcesLock.Unlock()

	// clean up watcher
	source.cancel()

	return source.Conn.Close()
}

func (s *SourceStore) AsClients(namespaces ...string) map[registry.CatalogKey]registry.ClientInterface {
	refs := map[registry.CatalogKey]registry.ClientInterface{}
	s.sourcesLock.RLock()
	defer s.sourcesLock.RUnlock()
	for key, source := range s.sources {
		if source.LastConnect.IsZero() {
			continue
		}
		for _, namespace := range namespaces {
			if key.Namespace == namespace {
				refs[key] = registry.NewClientFromConn(source.Conn)
			}
		}
	}

	// TODO : remove unhealthy
	return refs
}

func (s *SourceStore) ClientsForNamespaces(namespaces ...string) map[registry.CatalogKey]client.Interface {
	refs := map[registry.CatalogKey]client.Interface{}
	s.sourcesLock.RLock()
	defer s.sourcesLock.RUnlock()
	for key, source := range s.sources {
		if source.LastConnect.IsZero() {
			continue
		}
		for _, namespace := range namespaces {
			if key.Namespace == namespace {
				refs[key] = client.NewClientFromConn(source.Conn)
			}
		}
	}

	// TODO : remove unhealthy
	return refs
}
