package catalog

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/apis/alphacatalogentry/v1alpha1"
	csvv1alpha1 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/client"
)

// CatalogSync tracks information about the last time the catalog was synced to the cluster
type CatalogSync struct {
	StartTime      metav1.Time
	EndTime        metav1.Time
	Status         string
	ServicesFound  int
	ServicesSynced int
	ServicesFailed int
	Errors         []error
}

// CustomResourceCatalogStore stores service Catalog entries as CRDs in the cluster
type CustomResourceCatalogStore struct {
	Client             client.AlphaCatalogEntryInterface
	Namespace          string
	LastSuccessfulSync CatalogSync
	LastAttemptedSync  CatalogSync
}

// Store creates a new AlphaCatalogEntry custom resource for the given service definition, csv
func (store *CustomResourceCatalogStore) Store(csv *csvv1alpha1.ClusterServiceVersion) (*v1alpha1.AlphaCatalogEntry, error) {
	spec := &v1alpha1.AlphaCatalogEntrySpec{ClusterServiceVersionSpec: csv.Spec}
	resource := v1alpha1.NewAlphaCatalogEntryResource(spec)
	csv.ObjectMeta.DeepCopyInto(&resource.ObjectMeta)
	resource.SetNamespace(store.Namespace)
	return store.Client.UpdateEntry(resource)
}

func (c CatalogSync) Error() string {
	return fmt.Sprintf("catalog sync failed: %d/%d services synced, %d/%d failures -- %v",
		c.ServicesFound, c.ServicesSynced, c.ServicesFailed, c.ServicesFound, c.Errors)
}

// Sync creates AlphaCatalogEntry CRDs for each entry in the catalog. Fails immediately on error.
func (store *CustomResourceCatalogStore) Sync(catalog Source) ([]*v1alpha1.AlphaCatalogEntry, error) {
	status := CatalogSync{
		StartTime: metav1.Now(),
		Status:    "syncing",
	}
	log.Debug("Catalog Sync       -- BEGIN")
	entries := []*v1alpha1.AlphaCatalogEntry{}
	csvs, err := catalog.ListServices()
	if err != nil {
		status.EndTime = metav1.Now()
		status.Errors = []error{fmt.Errorf("catalog ListServices error: %v", err)}
		status.Status = "error"
		log.Debugf("Catalog Sync -- ERROR %v", status.Errors)
		return entries, status
	}
	status.ServicesFound = len(csvs)
	for i, csv := range csvs {
		log.Debugf("Catalog Sync [%2d/%d] -- BEGIN store service %s v%s -- ", i+1, len(csvs),
			csv.GetName(), csv.Spec.Version)
		resource, err := store.Store(&csv)
		if err != nil {
			status.Errors = append(status.Errors, fmt.Errorf("error storing service %s v%s: %v",
				csv.GetName(), csv.Spec.Version, err))
			log.Debugf("Catalog Sync [%2d/%d] -- ERROR storing service %s -- %s", i+1, len(csvs),
				csv.GetName(), err)
			status.ServicesFailed = status.ServicesFailed + 1
			continue
		}
		status.ServicesSynced = status.ServicesSynced + 1
		log.Debugf("Catalog Sync [%2d/%d] -- OK    storing service %s v%s", i+1, len(csvs),
			csv.GetName(), csv.Spec.Version)
		entries = append(entries, resource)
	}
	status.EndTime = metav1.Now()
	store.LastAttemptedSync = status
	if status.Status != "error" {
		status.Status = "success"
		store.LastSuccessfulSync = status
	}
	log.Debugf("Catalog Sync -- END %d/%d services synced",
		status.ServicesSynced, status.ServicesFound)
	return entries, nil
}
