package catalog

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	opcache "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/cache"
)

// metaViewer is a transparent viewer that can be used as a basis for more complex viewers.
// It holds a set of commonly used utilities.
// TODO: we probably don't want to embed Operator.
type metaViewer struct {
	*Operator
	now       func() metav1.Time
	namespace string
}

var _ opcache.Viewer = &metaViewer{}

func (viewer *metaViewer) Key(obj interface{}) (key string, err error) {
	// Use the most common key func (namespace/name)
	// TODO: could we use metaViewer to store anything that implements meta.Interface by default?
	return cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
}

func (viewer *metaViewer) KeyByView(view interface{}) (key string, err error) {
	// Use the most common key func (namespace/name)
	// TODO: could we use metaViewer to store anything that implements meta.Interface by default?
	return cache.DeletionHandlingMetaNamespaceKeyFunc(view)
}

func (viewer *metaViewer) View(obj interface{}) (view interface{}, err error) {
	// Passthrough
	view = obj
	return
}

type metaViewerOption func(*metaViewer)

func withNamespace(namespace string) metaViewerOption {
	return func(viewer *metaViewer) {
		viewer.namespace = namespace
	}
}

// newMetaViewer returns a new metaViewer.
func newMetaViewer(op *Operator, options ...metaViewerOption) *metaViewer {
	viewer := &metaViewer{
		Operator:  op,
		now:       timeNow,
		namespace: op.namespace,
	}

	for _, option := range options {
		option(viewer)
	}

	return viewer
}
