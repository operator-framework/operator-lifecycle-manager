package indexer

import (
	"fmt"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"k8s.io/client-go/tools/cache"
)

const (
	// PresentCatalogIndexFuncKey is the recommended key to use for registering
	// the index func with an indexer.
	PresentCatalogIndexFuncKey string = "presentcatalogindexfunc"
)

// PresentCatalogIndexFunc returns index from CatalogSource/CatalogSourceNamespace
// of the given object (Subscription)
func PresentCatalogIndexFunc(obj interface{}) ([]string, error) {
	sub, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		return []string{""}, fmt.Errorf("invalid object of type: %T", obj)
	}

	if sub.Spec.CatalogSource != "" && sub.Spec.CatalogSourceNamespace != "" {
		return []string{sub.Spec.CatalogSource + "/" + sub.Spec.CatalogSourceNamespace}, nil
	}

	return []string{""}, nil
}

// CatalogSubscriberNamespaces returns the list of namespace (as a map with namespace as key)
// which has Suscriptions(s) that subscribe(s) to a given CatalogSource (name/namespace)
func CatalogSubscriberNamespaces(indexers map[string]cache.Indexer, name, namespace string) (map[string]struct{}, error) {
	nsSet := map[string]struct{}{}
	index := fmt.Sprintf("%s/%s", name, namespace)

	for _, indexer := range indexers {
		subs, err := indexer.ByIndex(PresentCatalogIndexFuncKey, index)
		if err != nil {
			return nil, err
		}
		for _, item := range subs {
			s, ok := item.(*v1alpha1.Subscription)
			if !ok {
				continue
			}
			// Add to set
			nsSet[s.GetNamespace()] = struct{}{}
		}
	}

	return nsSet, nil
}
