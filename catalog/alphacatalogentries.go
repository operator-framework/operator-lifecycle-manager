package catalog

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/apis/alphacatalogentry/v1alpha1"
	csvv1alpha1 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/client"
)

// CustomResourceCatalogStore stores service Catalog entries as CRDs in the cluster
type CustomResourceCatalogStore struct {
	client     client.AlphaCatalogEntryInterface
	syncedTime metav1.Time
}

// Store creates a new AlphaCatalogEntry custom resource for the given service definition, csv
func (store *CustomResourceCatalogStore) Store(csv *csvv1alpha1.ClusterServiceVersion) (*v1alpha1.AlphaCatalogEntry, error) {
	spec := &v1alpha1.AlphaCatalogEntrySpec{csv.Spec}
	resource := v1alpha1.NewAlphaCatalogEntryResource(spec)
	csv.ObjectMeta.DeepCopyInto(&resource.ObjectMeta)
	return store.client.UpdateEntry(&resource)
}

// Sync creates AlphaCatalogEntry CRDs for each entry in the catalog. Fails immediately on error.
func (store *CustomResourceCatalogStore) Sync(catalog Source) ([]*v1alpha1.AlphaCatalogEntry, error) {
	entries := []v1alpha1.AlphaCatalogEntry{}
	csvs, err := catalog.ListServices()
	if err != nil {
		return entries, fmt.Errorf("catalog sync failed: catalog ListServices error: %v", err)
	}
	for _, csv := range csvs {
		resource, err := store.Store(&csv)
		if err != nil {
			return entries, fmt.Errorf("catalog sync failed: error storing service %s v%s: %v",
				csv.GetName(), csv.Spec.Version, err)
		}
		entries = append(entries, resource)
	}
	store.syncedTime = metav1.Now()
	return entries, nil
}
