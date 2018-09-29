package packagemanifest

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/provider"
)

type Watcher struct {
	namespace       string
	name            string
	resourceVersion string
	labelSelector   labels.Selector

	source provider.PackageManifestProvider

	stopped bool
	stop    chan struct{}
	result  chan watch.Event
	mu      sync.Mutex
}

var _ watch.Interface = &Watcher{}

func NewWatcher(namespace, name, resourceVersion string, labelSelector labels.Selector, source provider.PackageManifestProvider) *Watcher {
	return &Watcher{
		namespace:       namespace,
		resourceVersion: resourceVersion,
		labelSelector:   labelSelector,
		source:          source,
		stopped:         false,
		stop:            make(chan struct{}),
		result:          make(chan watch.Event),
		mu:              sync.Mutex{},
	}
}

// Run is a blocking method which starts the watch by subscribing to the source.
// Should run in a goroutine.
func (w *Watcher) Run(ctx context.Context) {
	add, modify, delete, err := w.source.Subscribe(w.namespace, w.stop)
	if err != nil {
		return
	}

	for {
		select {
		case manifest := <-add:
			w.Add(manifest)
		case manifest := <-modify:
			w.Modify(manifest)
		case manifest := <-delete:
			w.Delete(manifest)
		case <-w.stop:
		case <-ctx.Done():
			return
		}
	}
}

func (w *Watcher) Stop() {
	w.stop <- struct{}{}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stopped {
		close(w.result)
		w.stopped = true
	}
}

func (w *Watcher) ResultChan() <-chan watch.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

func (w *Watcher) Add(manifest v1alpha1.PackageManifest) {
	// TODO: Handle `resourceVersion`
	if matches(manifest, w.name, w.labelSelector) {
		w.send(watch.Event{Type: watch.Added, Object: &manifest})
	}
}

func (w *Watcher) Modify(manifest v1alpha1.PackageManifest) {
	// TODO: Handle `resourceVersion`
	if matches(manifest, w.name, w.labelSelector) {
		w.send(watch.Event{Type: watch.Modified, Object: &manifest})
	}
}

func (w *Watcher) Delete(lastValue v1alpha1.PackageManifest) {
	// TODO: Handle `resourceVersion`
	if matches(lastValue, w.name, w.labelSelector) {
		w.send(watch.Event{Type: watch.Deleted, Object: &lastValue})
	}
}

func (w *Watcher) send(e watch.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stopped {
		w.result <- e
	}
}
