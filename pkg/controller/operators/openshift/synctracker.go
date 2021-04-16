package openshift

import (
	"context"
	"fmt"
	"sync"

	configv1 "github.com/openshift/api/config/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func NewSyncTracker(syncCh <-chan error, co *configv1.ClusterOperator) *SyncTracker {
	return &SyncTracker{
		syncCh: syncCh,
		events: make(chan event.GenericEvent),
		co:     co,
	}
}

type SyncTracker struct {
	syncCh <-chan error
	events chan event.GenericEvent
	mutex  sync.RWMutex
	once   sync.Once

	co                          *configv1.ClusterOperator
	totalSyncs, successfulSyncs int
}

func (s *SyncTracker) Start(ctx context.Context) error {
	if s.syncCh == nil || s.events == nil || s.co == nil {
		return fmt.Errorf("invalid %T fields", s)
	}

	var err error
	s.once.Do(func() {
		err = s.start(ctx)
	})

	return err

}

func (s *SyncTracker) start(ctx context.Context) error {
	defer close(s.events)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-s.syncCh:
			if !ok {
				// Channel is closed
				return nil
			}

			s.addSync(err == nil)
		}
		s.events <- event.GenericEvent{Object: s.co}
	}
}

func (s *SyncTracker) Events() <-chan event.GenericEvent {
	return s.events
}

func (s *SyncTracker) addSync(successful bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.totalSyncs++
	if successful {
		s.successfulSyncs++
	}
}

func (s *SyncTracker) TotalSyncs() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	return s.totalSyncs
}

func (s *SyncTracker) SuccessfulSyncs() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	return s.successfulSyncs
}
