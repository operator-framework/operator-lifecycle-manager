package openshift

import (
	"context"
	"fmt"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestInvalidFields(t *testing.T) {
	// Create a cancelled context that we can signal the tracker to shutdown immediately after Start is called
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	// Attempt to start with missing fields
	tracker := &SyncTracker{}
	require.Error(t, tracker.Start(cancelled))

	// Add missing fields, then try again
	tracker.syncCh = make(chan error)
	tracker.events = make(chan event.GenericEvent)
	tracker.co = &ClusterOperator{}
	require.NoError(t, tracker.Start(cancelled))
}

func TestSyncCount(t *testing.T) {
	f := func(failed, successful uint8) bool {
		syncCh := make(chan error)
		defer close(syncCh)

		co := NewClusterOperator("operator")
		tracker := NewSyncTracker(syncCh, co)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			require.NoError(t, tracker.Start(ctx))
		}()

		go func() {
			err := fmt.Errorf("failure!")
			f := uint(failed)
			s := uint(successful)
			for f > 0 || s > 0 {
				if f > 0 {
					syncCh <- err
					f--
					continue
				}
				syncCh <- nil
				s--
			}
		}()

		total := int(failed) + int(successful)
		received := 0
		for range tracker.Events() {
			received++
			if received >= total {
				break
			}
		}

		require.Equal(t, int(successful), tracker.SuccessfulSyncs(), "incorrect amount of successful sync messages received")
		require.Equal(t, total, tracker.TotalSyncs(), "incorrect total amount of sync messages received")

		return true
	}

	require.NoError(t, quick.Check(f, nil))
}
