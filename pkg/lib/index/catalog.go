package indexer

import (
	"fmt"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
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
	indicies := []string{}

	sub, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		return indicies, fmt.Errorf("invalid object of type: %T", obj)
	}

	indicies = append(indicies, fmt.Sprintf("%s/%s", sub.Spec.CatalogSource,
		sub.Spec.CatalogSourceNamespace))

	return indicies, nil
}

// CatalogSubscriberNamespaces returns the list of Suscriptions' name and namespace
// (name/namespace as key and namespace as value) that uses the given CatalogSource (name/namespace)
func CatalogSubscriberNamespaces(indexers map[string]cache.Indexer, name, namespace string) (map[string]string, error) {
	nsSet := map[string]string{}
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
			key := fmt.Sprintf("%s/%s", s.GetName(), s.GetNamespace())
			nsSet[key] = s.GetNamespace()
		}
	}

	return nsSet, nil
}
