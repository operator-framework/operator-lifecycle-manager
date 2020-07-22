//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_registry_store.go ../../../../vendor/github.com/operator-framework/operator-registry/pkg/registry/interface.go Query
package grpc

import (
	"context"
	"fmt"
	"net"
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

func server(store opregistry.Query, port int) (func(), func()) {
	lis, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
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

	return serve, stop
}

type FakeSourceSyncer struct {
	// using a map[int] to preserve order
	History map[registry.CatalogKey][]connectivity.State

	sync.Mutex
	expectedEvents int
	done           chan struct{}
}

func (f *FakeSourceSyncer) sync(state SourceState) {
	f.Lock()
	if f.History[state.Key] == nil {
		f.History[state.Key] = []connectivity.State{}
	}
	f.History[state.Key] = append(f.History[state.Key], state.State)
	f.expectedEvents -= 1
	if f.expectedEvents == 0 {
		f.done <- struct{}{}
	}
	f.Unlock()
}

func NewFakeSourceSyncer(expectedEvents int) *FakeSourceSyncer {
	return &FakeSourceSyncer{
		History:        map[registry.CatalogKey][]connectivity.State{},
		expectedEvents: expectedEvents,
		done:           make(chan struct{}),
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
			totalEvents := 0
			port := 50050
			for _, events := range tt.expectedHistory {
				totalEvents += len(events)
				port += 1
				serve, stop := server(&fakes.FakeQuery{}, port)
				go serve()
				defer stop()
			}

			// start source manager
			syncer := NewFakeSourceSyncer(totalEvents)
			sources := NewSourceStore(logrus.New(), 1*time.Second, 5*time.Second, syncer.sync)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			sources.Start(ctx)

			// add source for each catalog
			port = 50050
			for catalog := range tt.expectedHistory {
				port += 1
				_, err := sources.Add(catalog, fmt.Sprintf("localhost:%d", port))
				require.NoError(t, err)
			}

			// wait for syncing to finish
			<-syncer.done

			// verify sync events
			for catalog, events := range tt.expectedHistory {
				recordedEvents := syncer.History[catalog]
				for i := 0; i < len(recordedEvents); i++ {
					require.Equal(t, (events[i]).String(), (recordedEvents[i]).String())
				}
			}
		}
	}

	cases := []testcase{
		{
			name: "Basic",
			expectedHistory: map[registry.CatalogKey][]connectivity.State{
				registry.CatalogKey{Name: "test", Namespace: "test"}: {
					connectivity.Connecting,
					connectivity.Ready,
				},
			},
		},
		{
			name: "Multiple",
			expectedHistory: map[registry.CatalogKey][]connectivity.State{
				registry.CatalogKey{Name: "test", Namespace: "test"}: {
					connectivity.Connecting,
					connectivity.Ready,
				},
				registry.CatalogKey{Name: "test2", Namespace: "test2"}: {
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
