package packagemanifest

import (
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
)

type PackageManifestWatcher struct {
	namespace     string
	labelSelector labels.Selector
	stop          chan struct{}
	results       chan watch.Event
	in            <-chan v1alpha1.PackageManifest
}

func NewPackageManifestWatcher(namespace string, labelSelector labels.Selector) *PackageManifestWatcher {
	watcher := &PackageManifestWatcher{
		namespace:     namespace,
		labelSelector: labelSelector,
		stop:          make(chan struct{}),
		results:       make(chan watch.Event),
		in:            make(<-chan v1alpha1.PackageManifest),
	}

	return watcher
}

func (w *PackageManifestWatcher) Run() {
	go func() {
		for {
			select {
			case manifest := <-w.in:
				if w.labelSelector.Matches(labels.Set(manifest.GetLabels())) {
					event := watch.Event{
						Type:   watch.Modified,
						Object: &manifest,
					}
					w.results <- event
				}
			case <-w.stop:
				return
			}
		}
	}()
}

// Stop stops the running watch
func (w *PackageManifestWatcher) Stop() {
	w.stop <- struct{}{}
}

// ResultChan returns the result chan
func (w *PackageManifestWatcher) ResultChan() <-chan watch.Event {
	return w.results
}
