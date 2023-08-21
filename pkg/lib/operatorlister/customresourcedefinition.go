package operatorlister

import (
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/metadata/metadatalister"
)

// UnionCustomResourceDefinitionLister is a custom implementation of an CustomResourceDefinition lister that allows a new
// Lister to be registered on the fly.
type UnionCustomResourceDefinitionLister struct {
	CustomResourceDefinitionLister metadatalister.Lister
	CustomResourceDefinitionLock   sync.RWMutex
}

func (ucl *UnionCustomResourceDefinitionLister) Namespace(namespace string) metadatalister.NamespaceLister {
	ucl.CustomResourceDefinitionLock.RLock()
	defer ucl.CustomResourceDefinitionLock.RUnlock()

	if ucl.CustomResourceDefinitionLister == nil {
		panic(fmt.Errorf("no CustomResourceDefinition lister registered"))
	}
	return ucl.CustomResourceDefinitionLister.Namespace(namespace)
}

// List lists all CustomResourceDefinitions in the indexer.
func (ucl *UnionCustomResourceDefinitionLister) List(selector labels.Selector) (ret []*metav1.PartialObjectMetadata, err error) {
	ucl.CustomResourceDefinitionLock.RLock()
	defer ucl.CustomResourceDefinitionLock.RUnlock()

	if ucl.CustomResourceDefinitionLister == nil {
		return nil, fmt.Errorf("no CustomResourceDefinition lister registered")
	}
	return ucl.CustomResourceDefinitionLister.List(selector)
}

// Get retrieves the CustomResourceDefinition with the given name
func (ucl *UnionCustomResourceDefinitionLister) Get(name string) (*metav1.PartialObjectMetadata, error) {
	ucl.CustomResourceDefinitionLock.RLock()
	defer ucl.CustomResourceDefinitionLock.RUnlock()

	if ucl.CustomResourceDefinitionLister == nil {
		return nil, fmt.Errorf("no CustomResourceDefinition lister registered")
	}
	return ucl.CustomResourceDefinitionLister.Get(name)
}

// RegisterCustomResourceDefinitionLister registers a new CustomResourceDefinitionLister
func (ucl *UnionCustomResourceDefinitionLister) RegisterCustomResourceDefinitionLister(lister metadatalister.Lister) {
	ucl.CustomResourceDefinitionLock.Lock()
	defer ucl.CustomResourceDefinitionLock.Unlock()

	ucl.CustomResourceDefinitionLister = lister
}

func (l *apiExtensionsV1Lister) RegisterCustomResourceDefinitionLister(lister metadatalister.Lister) {
	l.customResourceDefinitionLister.RegisterCustomResourceDefinitionLister(lister)
}

func (l *apiExtensionsV1Lister) CustomResourceDefinitionLister() metadatalister.Lister {
	return l.customResourceDefinitionLister
}
