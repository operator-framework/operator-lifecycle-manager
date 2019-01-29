package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

const eventsMax = 10

type checker func(obj runtime.Object) bool

func retry(do func() error, times int) {
	tries := 0
	for tries < times {
		err := do()
		if err == nil {
			return
		}
		tries = tries + 1
	}
}

func awaitDeleted(t *testing.T, listerWatcher cache.ListerWatcher, name, namespace string) error {
	list, err := listerWatcher.List(metav1.ListOptions{FieldSelector: "metadata.name=" + name})
	require.NoError(t, err)

	items, err := meta.ExtractList(list)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		t.Logf("%s not found on initial list, skipping watch", name)
		return nil
	}

	listMeta, err := meta.ListAccessor(list)
	if err != nil {
		return nil
	}

	recordedEvents := []watch.Event{}
	recordEvent := func(event watch.Event) watch.Event {
		t.Logf("received %s event while waiting for %s to be deleted", string(event.Type), name)
		if len(recordedEvents) > eventsMax {
			t.Logf("observed more than %d events before %s was deleted", len(recordedEvents), name)
		}
		return event
	}

	results := func() <-chan watch.Event {
		ch := make(chan watch.Event)

		go func() {
			retry(func() error {
				watcher, err := listerWatcher.Watch(metav1.ListOptions{
					ResourceVersion: listMeta.GetResourceVersion(),
					FieldSelector:   "metadata.name=" + name,
				})
				require.NoError(t, err)

				evts := watcher.ResultChan()
				for {
					evt, ok := <-evts
					if !ok {
						return fmt.Errorf("Watch channel closed for %s in %s", name, namespace)
					}
					ch <- evt
				}
			}, 1)
		}()

		return ch
	}

	events := results()
	for {
		select {
		case evt := <-events:
			recordedEvents = append(recordedEvents, recordEvent(evt))
			if evt.Type == watch.Deleted {
				return nil
			}
		case <-time.After(pollDuration):
			return fmt.Errorf("Object %s was not deleted before deadline", name)
		}
	}
}

func awaitObject(t *testing.T, listerWatcher cache.ListerWatcher, name, namespace string, check checker) (runtime.Object, error) {
	list, err := listerWatcher.List(metav1.ListOptions{FieldSelector: "metadata.name=" + name})
	if err != nil {
		return nil, err
	}

	items, err := meta.ExtractList(list)
	if err != nil {
		return nil, err
	}
	if len(items) == 1 && check(items[0]) {
		t.Logf("found %s which passed check on initial list, skipping watch", name)
		return items[0], nil
	}

	listMeta, err := meta.ListAccessor(list)
	if err != nil {
		return nil, err
	}

	recordedEvents := []watch.Event{}
	recordEvent := func(event watch.Event) watch.Event {
		t.Logf("received %s event while waiting for %s to pass check", string(event.Type), name)
		if len(recordedEvents) > eventsMax {
			t.Logf("observed more than %d events before %s passed check", len(recordedEvents), name)
		}
		return event
	}

	results := func() <-chan watch.Event {
		ch := make(chan watch.Event)

		go func() {
			retry(func() error {
				watcher, err := listerWatcher.Watch(metav1.ListOptions{
					ResourceVersion: listMeta.GetResourceVersion(),
					FieldSelector:   "metadata.name=" + name,
				})
				require.NoError(t, err)

				evts := watcher.ResultChan()
				for {
					evt, ok := <-evts
					if !ok {
						return fmt.Errorf("Watch channel closed for %s in %s", name, namespace)
					}
					ch <- evt
				}
			}, 1)
		}()

		return ch
	}

	events := results()
	for {
		select {
		case evt := <-events:
			recordedEvents = append(recordedEvents, recordEvent(evt))
			if (evt.Type == watch.Added || evt.Type == watch.Modified) && check(evt.Object) {
				return evt.Object, nil
			}
		case <-time.After(pollDuration):
			return nil, fmt.Errorf("timed out waiting for object %s to match expected condition", name)
		}
	}
}
