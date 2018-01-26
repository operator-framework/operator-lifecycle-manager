package catalog

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
	"github.com/coreos-inc/alm/pkg/client"
)

const (
	CSVCatalogVisibilityAnnotation = "tectonic-visibility"
	CatalogEntryVisibilityLabel    = "tectonic-visibility"

	CatalogEntryVisibilityTectonicFeature = "tectonic-feature"
	CatalogEntryVisibilityOCS             = "ocs"
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
	Client             client.UICatalogEntryInterface
	Namespace          string
	LastSuccessfulSync CatalogSync
	LastAttemptedSync  CatalogSync
}

// Store creates a new UICatalogEntry custom resource for the given service definition, csv
func (store *CustomResourceCatalogStore) Store(manifest v1alpha1.PackageManifest, csv *csvv1alpha1.ClusterServiceVersion) (*v1alpha1.UICatalogEntry, error) {
	spec := &v1alpha1.UICatalogEntrySpec{Manifest: manifest, CSVSpec: csv.Spec}
	visibility, ok := csv.GetAnnotations()[CSVCatalogVisibilityAnnotation]
	if !ok {
		visibility = CatalogEntryVisibilityOCS // default to visible in catalog
	}
	resource := v1alpha1.NewUICatalogEntryResource(spec)
	csv.ObjectMeta.DeepCopyInto(&resource.ObjectMeta)
	resource.SetNamespace(store.Namespace)
	labels := resource.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[CatalogEntryVisibilityLabel] = visibility
	resource.SetLabels(labels)
	return store.Client.UpdateEntry(resource)
}

func (c CatalogSync) Error() string {
	return fmt.Sprintf("catalog sync failed: %d/%d services synced, %d/%d failures -- %v",
		c.ServicesFound, c.ServicesSynced, c.ServicesFailed, c.ServicesFound, c.Errors)
}

// Sync creates UICatalogEntry CRDs for each package in the catalog. Fails immediately on error.
func (store *CustomResourceCatalogStore) Sync(catalog Source) ([]*v1alpha1.UICatalogEntry, error) {
	status := CatalogSync{
		StartTime: metav1.Now(),
		Status:    "syncing",
	}
	log.Debug("Catalog Sync       -- BEGIN")
	entries := []*v1alpha1.UICatalogEntry{}
	status.ServicesFound = len(catalog.AllPackages())

	for name, manifest := range catalog.AllPackages() {
		log.Debugf("Catalog Sync -- BEGIN store service %s v%s -- ", name)
		latestCSVInDefaultChannel, err := catalog.FindCSVForPackageNameUnderChannel(name, manifest.GetDefaultChannel())
		if err != nil {
			status.Errors = append(status.Errors, fmt.Errorf("error getting service %s v%s: %v",
				latestCSVInDefaultChannel.GetName(), latestCSVInDefaultChannel.Spec.Version, err))
			log.Debugf("Catalog Sync -- ERROR getting service %s -- %s",
				latestCSVInDefaultChannel.GetName(), err)
		}
		resource, err := store.Store(manifest, latestCSVInDefaultChannel)
		if err != nil {
			status.Errors = append(status.Errors, fmt.Errorf("error storing service %s v%s: %v",
				latestCSVInDefaultChannel.GetName(), latestCSVInDefaultChannel.Spec.Version, err))
			log.Debugf("Catalog Sync -- ERROR storing service %s -- %s",
				latestCSVInDefaultChannel.GetName(), err)
			status.ServicesFailed = status.ServicesFailed + 1
			continue
		}
		status.ServicesSynced = status.ServicesSynced + 1
		log.Debugf("Catalog Sync -- OK    storing service %s v%s",
			latestCSVInDefaultChannel.GetName(), latestCSVInDefaultChannel.Spec.Version)
		entries = append(entries, resource)
	}

	status.EndTime = metav1.Now()
	if status.ServicesFound == status.ServicesSynced {
		status.Status = "success"
		store.LastSuccessfulSync = status
	} else {
		status.Status = "error"
	}
	store.LastAttemptedSync = status
	log.Debugf("Catalog Sync -- END %d/%d services synced",
		status.ServicesSynced, status.ServicesFound)
	return entries, nil
}
