package operatorlister

import (
	"fmt"
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	aextv1 "k8s.io/apiextensions-apiserver/pkg/client/listers/apiextensions/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// UnionCustomResourceDefinitionLister is a custom implementation of an CustomResourceDefinition lister that allows a new
// Lister to be registered on the fly. This Lister lists both v1 and v1beta1 APIVersion (at the newer version) CRDs.
type UnionCustomResourceDefinitionLister struct {
	CustomResourceDefinitionLister aextv1.CustomResourceDefinitionLister
	CustomResourceDefinitionLock   sync.RWMutex
}

// List lists all CustomResourceDefinitions in the indexer.
func (ucl *UnionCustomResourceDefinitionLister) List(selector labels.Selector) (ret []*apiextensionsv1.CustomResourceDefinition, err error) {
	ucl.CustomResourceDefinitionLock.RLock()
	defer ucl.CustomResourceDefinitionLock.RUnlock()

	if ucl.CustomResourceDefinitionLister == nil {
		return nil, fmt.Errorf("no CustomResourceDefinition lister registered")
	}
	return ucl.CustomResourceDefinitionLister.List(selector)
}

// Get retrieves the CustomResourceDefinition with the given name
func (ucl *UnionCustomResourceDefinitionLister) Get(name string) (*apiextensionsv1.CustomResourceDefinition, error) {
	ucl.CustomResourceDefinitionLock.RLock()
	defer ucl.CustomResourceDefinitionLock.RUnlock()

	if ucl.CustomResourceDefinitionLister == nil {
		return nil, fmt.Errorf("no CustomResourceDefinition lister registered")
	}
	return ucl.CustomResourceDefinitionLister.Get(name)
}

// RegisterCustomResourceDefinitionLister registers a new CustomResourceDefinitionLister
func (ucl *UnionCustomResourceDefinitionLister) RegisterCustomResourceDefinitionLister(lister aextv1.CustomResourceDefinitionLister) {
	ucl.CustomResourceDefinitionLock.Lock()
	defer ucl.CustomResourceDefinitionLock.Unlock()

	ucl.CustomResourceDefinitionLister = lister
}

func (l *apiExtensionsV1Lister) RegisterCustomResourceDefinitionLister(lister aextv1.CustomResourceDefinitionLister) {
	l.customResourceDefinitionLister.RegisterCustomResourceDefinitionLister(lister)
}

func (l *apiExtensionsV1Lister) CustomResourceDefinitionLister() aextv1.CustomResourceDefinitionLister {
	return l.customResourceDefinitionLister
}
