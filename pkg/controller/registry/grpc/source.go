package grpc

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-registry/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SourceMeta struct {
	Address         string
	LastConnect     metav1.Time
	ConnectionState connectivity.State
}

type SourceState struct {
	Key   resolver.CatalogKey
	State connectivity.State
}

type SourceConn struct {
	SourceMeta
	Conn   *grpc.ClientConn
	cancel context.CancelFunc
}

type SourceStore struct {
	sync.Once
	sources      map[resolver.CatalogKey]SourceConn
	sourcesLock  sync.RWMutex
	syncFn       func(SourceState)
	logger       *logrus.Logger
	notify       chan SourceState
	timeout      time.Duration
	readyTimeout time.Duration
}

func NewSourceStore(logger *logrus.Logger, timeout, readyTimeout time.Duration, sync func(SourceState)) *SourceStore {
	return &SourceStore{
		sources:      make(map[resolver.CatalogKey]SourceConn),
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

func (s *SourceStore) GetMeta(key resolver.CatalogKey) *SourceMeta {
	s.sourcesLock.RLock()
	source, ok := s.sources[key]
	s.sourcesLock.RUnlock()
	if !ok {
		return nil
	}

	return &source.SourceMeta
}

func (s *SourceStore) Exists(key resolver.CatalogKey) bool {
	s.sourcesLock.RLock()
	_, ok := s.sources[key]
	s.sourcesLock.RUnlock()
	return ok
}

func (s *SourceStore) Get(key resolver.CatalogKey) *SourceConn {
	s.sourcesLock.RLock()
	source, ok := s.sources[key]
	s.sourcesLock.RUnlock()
	if !ok {
		return nil
	}
	return &source
}

func (s *SourceStore) Add(key resolver.CatalogKey, address string) (*SourceConn, error) {
	_ = s.Remove(key)

	conn, err := grpc.Dial(address, grpc.WithInsecure())
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

func (s *SourceStore) watch(ctx context.Context, key resolver.CatalogKey, source SourceConn) {
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

func (s *SourceStore) Remove(key resolver.CatalogKey) error {
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

func (s *SourceStore) AsClients(globalNamespace, localNamespace string) map[resolver.CatalogKey]client.Interface {
	refs := map[resolver.CatalogKey]client.Interface{}
	s.sourcesLock.RLock()
	defer s.sourcesLock.RUnlock()
	for key, source := range s.sources {
		if !(key.Namespace == globalNamespace || key.Namespace == localNamespace) {
			continue
		}
		if source.LastConnect.IsZero() {
			continue
		}
		refs[key] = client.NewClientFromConn(source.Conn)
	}

	// TODO : remove unhealthy
	return refs
}
