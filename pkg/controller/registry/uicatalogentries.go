package registry

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	catalogv1alpha1 "github.com/coreos-inc/alm/pkg/api/apis/catalogsource/v1alpha1"
	csvv1alpha1 "github.com/coreos-inc/alm/pkg/api/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/api/apis/uicatalogentry/v1alpha1"
	"github.com/coreos-inc/alm/pkg/api/client/clientset/versioned"
	"k8s.io/apimachinery/pkg/api/errors"
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
	Client             versioned.Interface
	Namespace          string
	LastSuccessfulSync CatalogSync
	LastAttemptedSync  CatalogSync
}

// Store creates a new UICatalogEntry custom resource for the given service definition, csv
func (store *CustomResourceCatalogStore) Store(manifest v1alpha1.PackageManifest, csv *csvv1alpha1.ClusterServiceVersion, ownerRefs []metav1.OwnerReference) (*v1alpha1.UICatalogEntry, error) {
	spec := &v1alpha1.UICatalogEntrySpec{Manifest: manifest, CSVSpec: csv.Spec}
	visibility, ok := csv.GetAnnotations()[CSVCatalogVisibilityAnnotation]
	if !ok {
		visibility = CatalogEntryVisibilityOCS // default to visible in catalog
	}
	resource := v1alpha1.NewUICatalogEntryResource(spec)
	resource.SetName(manifest.PackageName)
	resource.SetNamespace(store.Namespace)
	labels := resource.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[CatalogEntryVisibilityLabel] = visibility
	resource.SetLabels(labels)
	resource.SetOwnerReferences(ownerRefs)

	old, err := store.Client.UicatalogentryV1alpha1().UICatalogEntries(resource.GetNamespace()).Get(resource.GetName(), metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil, err
		}
		return store.Client.UicatalogentryV1alpha1().UICatalogEntries(resource.GetNamespace()).Create(resource)
	}
	// Set the resource version.
	resource.SetResourceVersion(old.GetResourceVersion())

	return store.Client.UicatalogentryV1alpha1().UICatalogEntries(resource.GetNamespace()).Update(resource)
}

func (c CatalogSync) Error() string {
	return fmt.Sprintf("catalog sync failed: %d/%d services synced, %d/%d failures -- %v",
		c.ServicesFound, c.ServicesSynced, c.ServicesFailed, c.ServicesFound, c.Errors)
}

// Sync creates UICatalogEntry CRDs for each package in the catalog and removes old ones. Fails immediately on error.
func (store *CustomResourceCatalogStore) Sync(catalog Source, source *catalogv1alpha1.CatalogSource) ([]*v1alpha1.UICatalogEntry, error) {
	status := CatalogSync{
		StartTime: metav1.Now(),
		Status:    "syncing",
	}
	source.TypeMeta = metav1.TypeMeta{
		Kind:       catalogv1alpha1.CatalogSourceKind,
		APIVersion: catalogv1alpha1.CatalogSourceCRDAPIVersion,
	}
	controllerRef := metav1.NewControllerRef(source, source.GroupVersionKind())
	ownerRefs := []metav1.OwnerReference{
		*controllerRef,
	}

	log.Debug("Catalog Sync -- BEGIN")
	entries := []*v1alpha1.UICatalogEntry{}
	status.ServicesFound = len(catalog.AllPackages())

	log.Debugf("Catalog Sync -- Packages found: %v", catalog.AllPackages())

	// fetch existing UICatalogEntries to prune any old ones
	existingEntries, err := store.Client.UicatalogentryV1alpha1().UICatalogEntries(store.Namespace).List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for name, manifest := range catalog.AllPackages() {
		log.Debugf("Catalog Sync -- BEGIN store service %s", name)
		latestCSVInDefaultChannel, err := catalog.FindCSVForPackageNameUnderChannel(name, manifest.GetDefaultChannel())
		if err != nil {
			status.Errors = append(status.Errors, fmt.Errorf("error getting service %s v%s: %v",
				latestCSVInDefaultChannel.GetName(), latestCSVInDefaultChannel.Spec.Version, err))
			log.Debugf("Catalog Sync -- ERROR getting service %s -- %s",
				latestCSVInDefaultChannel.GetName(), err)
			status.ServicesFailed = status.ServicesFailed + 1
			continue
		}
		resource, err := store.Store(manifest, latestCSVInDefaultChannel, ownerRefs)
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

	if existingEntries != nil {
		log.Debugf("Catalog Sync -- Pruning old services")
		store.prune(source, existingEntries.Items, entries)
	}

	log.Debugf("Catalog Sync -- END %d/%d services synced",
		status.ServicesSynced, status.ServicesFound)

	return entries, nil
}

func (store *CustomResourceCatalogStore) prune(source *catalogv1alpha1.CatalogSource, existingEntries, newEntries []*v1alpha1.UICatalogEntry) error {
	var immediateDelete int64 = 0
	existingMap := map[string]*v1alpha1.UICatalogEntry{}
	newMap := map[string]*v1alpha1.UICatalogEntry{}

	for _, existing := range existingEntries {
		// prune things controlled by this source
		if metav1.IsControlledBy(existing, source) {
			existingMap[existing.Name] = existing
		}

		// prune things not controlled by any source
		if existing.GetOwnerReferences() == nil || len(existing.GetOwnerReferences()) == 0 {
			existingMap[existing.Name] = existing
		}
	}
	for _, newEntry := range newEntries {
		newMap[newEntry.Name] = newEntry
	}

	for name := range existingMap {
		if _, ok := newMap[name]; !ok {
			if err := store.Client.UicatalogentryV1alpha1().UICatalogEntries(store.Namespace).Delete(name, &metav1.DeleteOptions{GracePeriodSeconds: &immediateDelete}); err != nil {
				log.Debugf("Catalog Sync -- err pruning %s: %s", name, err)
				return err
			}
			log.Debugf("Catalog Sync -- pruned %s", name)
		}
	}
	return nil
}
