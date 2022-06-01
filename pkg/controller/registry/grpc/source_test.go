//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_registry_store.go ../../../../vendor/github.com/operator-framework/operator-registry/pkg/registry/interface.go Query
package grpc

import (
	"context"
	"net"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
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
					connectivity.Idle,
				},
			},
		},
		{
			name: "Multiple",
			expectedHistory: map[registry.CatalogKey][]connectivity.State{
				{Name: "test", Namespace: "test"}: {
					connectivity.Connecting,
					connectivity.Ready,
					connectivity.Idle,
				},
				{Name: "test2", Namespace: "test2"}: {
					connectivity.Connecting,
					connectivity.Ready,
					connectivity.Idle,
				},
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, test(tt))
	}
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
