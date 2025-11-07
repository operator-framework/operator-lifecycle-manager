//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_registry_store.go ../../../../vendor/github.com/operator-framework/operator-registry/pkg/registry/interface.go Query
package grpc

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	opserver "github.com/operator-framework/operator-registry/pkg/server"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

func server(store opregistry.Query) (func(), string, func()) {
	lis, err := net.Listen("tcp", "localhost:")
	if err != nil {
		logrus.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()

	api.RegisterRegistryServer(s, opserver.NewRegistryServer(store))

	serve := func() {
		if err := s.Serve(lis); err != nil {
			logrus.Fatalf("failed to serve: %v", err)
		}
	}

	stop := func() {
		s.Stop()
	}

	return serve, lis.Addr().String(), stop
}

type FakeSourceSyncer struct {
	// using a map[int] to preserve order
	History map[registry.CatalogKey][]connectivity.State

	sync.Mutex
	expectedReadies int
	done            chan struct{}
}

func (f *FakeSourceSyncer) sync(state SourceState) {
	f.Lock()
	if f.History[state.Key] == nil {
		f.History[state.Key] = []connectivity.State{}
	}
	f.History[state.Key] = append(f.History[state.Key], state.State)
	if state.State == connectivity.Ready {
		f.expectedReadies--
	}
	if f.expectedReadies == 0 {
		f.done <- struct{}{}
	}
	f.Unlock()
}

func NewFakeSourceSyncer(expectedReadies int) *FakeSourceSyncer {
	return &FakeSourceSyncer{
		History:         map[registry.CatalogKey][]connectivity.State{},
		expectedReadies: expectedReadies,
		done:            make(chan struct{}),
	}
}

func TestConnectionEvents(t *testing.T) {
	type testcase struct {
		name            string
		expectedHistory map[registry.CatalogKey][]connectivity.State
	}

	test := func(tt testcase) func(t *testing.T) {
		return func(t *testing.T) {
			// start server for each catalog
			addresses := map[registry.CatalogKey]string{}

			for catalog := range tt.expectedHistory {
				serve, address, stop := server(&fakes.FakeQuery{})
				addresses[catalog] = address
				go serve()
				defer stop()
			}

			// start source manager
			syncer := NewFakeSourceSyncer(len(tt.expectedHistory))
			sources := NewSourceStore(logrus.New(), 1*time.Second, 5*time.Second, syncer.sync)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			sources.Start(ctx)

			// add source for each catalog
			for catalog, address := range addresses {
				_, err := sources.Add(catalog, address)
				require.NoError(t, err)
			}

			// wait for syncing to finish
			<-syncer.done

			// verify sync events
			for catalog, events := range tt.expectedHistory {
				recordedEvents := syncer.History[catalog]
				for i := 0; i < len(recordedEvents); i++ {
					found := false
					for _, event := range events {
						if event.String() == recordedEvents[i].String() {
							found = true
						}
					}
					require.True(t, found)
				}
			}
		}
	}

	cases := []testcase{
		{
			name: "Basic",
			expectedHistory: map[registry.CatalogKey][]connectivity.State{
				{Name: "test", Namespace: "test"}: {
					connectivity.Connecting,
					connectivity.Ready,
				},
			},
		},
		{
			name: "Multiple",
			expectedHistory: map[registry.CatalogKey][]connectivity.State{
				{Name: "test", Namespace: "test"}: {
					connectivity.Connecting,
					connectivity.Ready,
				},
				{Name: "test2", Namespace: "test2"}: {
					connectivity.Connecting,
					connectivity.Ready,
				},
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, test(tt))
	}
}

// Confirms the controller records failure when the registry endpoint cannot be reached.
func TestConnectionEventsRecordsFailureForUnreachableAddress(t *testing.T) {
	catalogKey := registry.CatalogKey{Name: "test", Namespace: "test"}

	syncer := NewFakeSourceSyncer(1)
	sources := NewSourceStore(logrus.New(), 500*time.Millisecond, time.Second, syncer.sync)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sources.Start(ctx)

	_, err := sources.Add(catalogKey, "127.0.0.1:65534")
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		syncer.Lock()
		defer syncer.Unlock()
		for _, state := range syncer.History[catalogKey] {
			if state == connectivity.TransientFailure {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "expected transient failure when catalog address is unreachable")
}

// Validates proxied connections succeed even when the client cannot resolve the cluster address.
func TestGrpcConnectionConnectsThroughProxyForClusterAddress(t *testing.T) {
	t.Setenv("GRPC_PROXY", "")
	t.Setenv("grpc_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	serve, backendAddr, stopServer := server(&fakes.FakeQuery{})
	go serve()
	defer stopServer()

	target := "service.namespace.svc:50051"

	directConn, err := grpcConnection(target)
	require.NoError(t, err)
	waitForState(t, directConn, 5*time.Second, func(state connectivity.State) bool {
		return state == connectivity.TransientFailure || state == connectivity.Shutdown
	})
	require.NoError(t, directConn.Close())

	dialer := setupTestProxyDialer(backendAddr)

	t.Setenv("GRPC_PROXY", "grpc-test://proxy")
	t.Setenv("NO_PROXY", "")

	proxyConn, err := grpcConnection(target)
	require.NoError(t, err)
	defer proxyConn.Close()

	waitForState(t, proxyConn, 10*time.Second, func(state connectivity.State) bool {
		return state == connectivity.Ready
	})

	require.Greater(t, dialer.Calls(), 0, "expected proxy dialer to be used")
	require.NotEmpty(t, dialer.LastAddr(), "expected proxy dialer to record address")
	require.True(t, strings.Contains(dialer.LastAddr(), target), "expected dial address to include target, got %q", dialer.LastAddr())
}

func waitForState(t *testing.T, conn *grpc.ClientConn, timeout time.Duration, match func(connectivity.State) bool) {
	t.Helper()

	var last connectivity.State
	require.Eventually(t, func() bool {
		conn.Connect()
		last = conn.GetState()
		if match(last) {
			return true
		}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		conn.WaitForStateChange(ctx, last)
		cancel()

		last = conn.GetState()
		return match(last)
	}, timeout, 100*time.Millisecond, fmt.Sprintf("connection never satisfied predicate; last state=%s", last))
}

var (
	registerTestProxyDialerOnce sync.Once
	testProxyDialer             = &recordingProxyDialer{}
)

func setupTestProxyDialer(backend string) *recordingProxyDialer {
	registerTestProxyDialerOnce.Do(func() {
		proxy.RegisterDialerType("grpc-test", func(u *url.URL, _ proxy.Dialer) (proxy.Dialer, error) {
			return testProxyDialer, nil
		})
	})
	testProxyDialer.Reset(backend)
	return testProxyDialer
}

type recordingProxyDialer struct {
	mu      sync.Mutex
	backend string
	addrs   []string
	calls   int
}

func (d *recordingProxyDialer) Dial(network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.calls++
	d.addrs = append(d.addrs, addr)
	backend := d.backend
	d.mu.Unlock()
	return net.Dial(network, backend)
}

func (d *recordingProxyDialer) Reset(backend string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.backend = backend
	d.addrs = nil
	d.calls = 0
}

func (d *recordingProxyDialer) Calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func (d *recordingProxyDialer) LastAddr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.addrs) == 0 {
		return ""
	}
	return d.addrs[len(d.addrs)-1]
}

func TestGetEnvAny(t *testing.T) {
	type envVar struct {
		key   string
		value string
	}

	type testcase struct {
		name          string
		envVars       []envVar
		expectedValue string
	}

	test := func(tt testcase) func(t *testing.T) {
		return func(t *testing.T) {
			for _, envVar := range tt.envVars {
				os.Setenv(envVar.key, envVar.value)
			}

			defer func() {
				for _, envVar := range tt.envVars {
					os.Setenv(envVar.key, "")
				}
			}()

			require.Equal(t, getEnvAny("NO_PROXY", "no_proxy"), tt.expectedValue)
		}
	}

	cases := []testcase{
		{
			name:          "NotFound",
			expectedValue: "",
		},
		{
			name: "LowerCaseFound",
			envVars: []envVar{
				{
					key:   "no_proxy",
					value: "foo",
				},
			},
			expectedValue: "foo",
		},
		{
			name: "UpperCaseFound",
			envVars: []envVar{
				{
					key:   "NO_PROXY",
					value: "bar",
				},
			},
			expectedValue: "bar",
		},
		{
			name: "OrderPreference",
			envVars: []envVar{
				{
					key:   "no_proxy",
					value: "foo",
				},
				{
					key:   "NO_PROXY",
					value: "bar",
				},
			},
			expectedValue: "bar",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, test(tt))
	}
}

func TestGetGRPCProxyEnv(t *testing.T) {
	type envVar struct {
		key   string
		value string
	}

	type testcase struct {
		name          string
		envVars       []envVar
		expectedValue string
	}

	test := func(tt testcase) func(t *testing.T) {
		return func(t *testing.T) {
			for _, envVar := range tt.envVars {
				os.Setenv(envVar.key, envVar.value)
			}

			defer func() {
				for _, envVar := range tt.envVars {
					os.Setenv(envVar.key, "")
				}
			}()

			require.Equal(t, getGRPCProxyEnv(), tt.expectedValue)
		}
	}

	cases := []testcase{
		{
			name:          "NotFound",
			expectedValue: "",
		},
		{
			name: "LowerCaseFound",
			envVars: []envVar{
				{
					key:   "grpc_proxy",
					value: "foo",
				},
			},
			expectedValue: "foo",
		},
		{
			name: "UpperCaseFound",
			envVars: []envVar{
				{
					key:   "GRPC_PROXY",
					value: "bar",
				},
			},
			expectedValue: "bar",
		},
		{
			name: "UpperCasePreference",
			envVars: []envVar{
				{
					key:   "grpc_proxy",
					value: "foo",
				},
				{
					key:   "GRPC_PROXY",
					value: "bar",
				},
			},
			expectedValue: "bar",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, test(tt))
	}
}

func TestGRPCProxyURL(t *testing.T) {
	type envVar struct {
		key   string
		value string
	}

	type testcase struct {
		name          string
		address       string
		envVars       []envVar
		expectedProxy string
		expectedError error
	}

	test := func(tt testcase) func(t *testing.T) {
		return func(t *testing.T) {
			for _, envVar := range tt.envVars {
				os.Setenv(envVar.key, envVar.value)
			}

			defer func() {
				for _, envVar := range tt.envVars {
					os.Setenv(envVar.key, "")
				}
			}()

			var expectedProxyURL *url.URL
			var err error
			if tt.expectedProxy != "" {
				expectedProxyURL, err = url.Parse(tt.expectedProxy)
				require.NoError(t, err)
			}

			proxyURL, err := grpcProxyURL(tt.address)
			require.Equal(t, expectedProxyURL, proxyURL)
			require.Equal(t, tt.expectedError, err)
		}
	}

	cases := []testcase{
		{
			name:          "NoGRPCProxySet",
			address:       "foo.com:8080",
			expectedProxy: "",
			expectedError: nil,
		},
		{
			name:    "GRPCProxyFoundForAddress",
			address: "foo.com:8080",
			envVars: []envVar{
				{
					key:   "GRPC_PROXY",
					value: "http://my-proxy:8080",
				},
			},
			expectedProxy: "http://my-proxy:8080",
			expectedError: nil,
		},
		{
			name:    "GRPCNoProxyIncludesAddress",
			address: "foo.com:8080",
			envVars: []envVar{
				{
					key:   "GRPC_PROXY",
					value: "http://my-proxy:8080",
				},
				{
					key:   "NO_PROXY",
					value: "foo.com:8080",
				},
			},
			expectedProxy: "",
			expectedError: nil,
		},
		{
			name:          "MissingPort",
			address:       "foo.com",
			expectedProxy: "",
			expectedError: error(&net.AddrError{Err: "missing port in address", Addr: "foo.com"}),
		},
		{
			name:          "TooManyColons",
			address:       "http://bar.com:8080",
			expectedProxy: "",
			expectedError: error(&net.AddrError{Err: "too many colons in address", Addr: "http://bar.com:8080"}),
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, test(tt))
	}
}
