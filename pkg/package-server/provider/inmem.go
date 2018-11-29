package provider

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	packagev1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
)

const (
	// ConfigMapPackageName is the key for package ConfigMap data
	ConfigMapPackageName = "packages"
	// ConfigMapCSVName is the key for CSV ConfigMap data
	ConfigMapCSVName = "clusterServiceVersions"
)

type packageKey struct {
	catalogSourceName      string
	catalogSourceNamespace string
	packageName            string
}

type eventChan struct {
	namespace string
	ch        chan packagev1alpha1.PackageManifest
}

// InMemoryProvider syncs and provides PackageManifests from the cluster using an in-memory cache.
// Should be a global singleton.
type InMemoryProvider struct {
	*queueinformer.Operator
	mu              sync.RWMutex
	globalNamespace string

	manifests map[packageKey]packagev1alpha1.PackageManifest

	add    []eventChan
	modify []eventChan
	delete []eventChan
}

// NewInMemoryProvider returns a pointer to a new InMemoryProvider instance
func NewInMemoryProvider(informers []cache.SharedIndexInformer, queueOperator *queueinformer.Operator, globalNS string) *InMemoryProvider {
	prov := &InMemoryProvider{
		Operator:        queueOperator,
		globalNamespace: globalNS,
		manifests:       make(map[packageKey]packagev1alpha1.PackageManifest),
	}

	queue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "catalogsources")
	queueInformers := queueinformer.New(
		queue,
		informers,
		prov.syncCatalogSource,
		nil,
		"catsrc",
		metrics.NewMetricsNil(),
		logrus.New(),
	)
	for _, informer := range queueInformers {
		prov.RegisterQueueInformer(informer)
	}

	return prov
}

// parsePackageManifestsFromConfigMap returns a list of PackageManifests from a given ConfigMap
func parsePackageManifestsFromConfigMap(cm *corev1.ConfigMap, catsrc *operatorsv1alpha1.CatalogSource) ([]packagev1alpha1.PackageManifest, error) {
	cmName := cm.GetName()
	logger := logrus.WithFields(logrus.Fields{
		"Action": "Load ConfigMap",
		"name":   cmName,
	})

	found := false
	csvs := make(map[string]operatorsv1alpha1.ClusterServiceVersion)
	csvListYaml, ok := cm.Data[ConfigMapCSVName]
	if ok {
		logger.Debug("ConfigMap contains CSVs")
		csvListJSON, err := yaml.YAMLToJSON([]byte(csvListYaml))
		if err != nil {
			logger.Debugf("Load ConfigMap     -- ERROR %s : error=%s", cmName, err)
			return nil, fmt.Errorf("error loading CSV list yaml from ConfigMap %s: %s", cmName, err)
		}

		var parsedCSVList []operatorsv1alpha1.ClusterServiceVersion
		err = json.Unmarshal([]byte(csvListJSON), &parsedCSVList)
		if err != nil {
			logger.Debugf("Load ConfigMap     -- ERROR %s : error=%s", cmName, err)
			return nil, fmt.Errorf("error parsing CSV list (json) from ConfigMap %s: %s", cmName, err)
		}

		for _, csv := range parsedCSVList {
			found = true

			// TODO: add check for invalid CSV definitions
			logger.Debugf("found csv %s", csv.GetName())
			csvs[csv.GetName()] = csv
		}
	}

	manifests := []packagev1alpha1.PackageManifest{}
	packageListYaml, ok := cm.Data[ConfigMapPackageName]
	if ok {
		logger.Debug("ConfigMap contains packages")
		packageListJSON, err := yaml.YAMLToJSON([]byte(packageListYaml))
		if err != nil {
			logger.Debugf("ERROR: %s", err)
			return nil, fmt.Errorf("error loading package list yaml from ConfigMap %s: %s", cmName, err)
		}

		var parsedStatuses []packagev1alpha1.PackageManifestStatus
		err = json.Unmarshal([]byte(packageListJSON), &parsedStatuses)
		if err != nil {
			logger.Debugf("ERROR: %s", err)
			return nil, fmt.Errorf("error parsing package list (json) from ConfigMap %s: %s", cmName, err)
		}

		for _, status := range parsedStatuses {
			found = true

			// add the name and namespace of the CatalogSource
			manifest := packagev1alpha1.PackageManifest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      status.PackageName,
					Namespace: cm.GetNamespace(),
					Labels:    map[string]string{},
				},
				Status: status,
			}

			manifest.Status.CatalogSource = catsrc.GetName()
			manifest.Status.CatalogSourceNamespace = catsrc.GetNamespace()
			manifest.Status.CatalogSourceDisplayName = catsrc.Spec.DisplayName
			manifest.Status.CatalogSourcePublisher = catsrc.Spec.Publisher

			// add all PackageChannel CSVDescriptions
			for i, channel := range manifest.Status.Channels {
				csv, ok := csvs[channel.CurrentCSV]
				if !ok {
					return nil, fmt.Errorf("packagemanifest %s references non-existent csv %s", manifest.Status.PackageName, channel.CurrentCSV)
				}

				manifest.Status.Channels[i].CurrentCSVDesc = packagev1alpha1.CreateCSVDescription(&csv)

				// set the Provider
				if manifest.Status.DefaultChannel != "" && csv.GetName() == manifest.Status.DefaultChannel || i == 0 {
					manifest.Status.Provider = packagev1alpha1.AppLink{
						Name: csv.Spec.Provider.Name,
						URL:  csv.Spec.Provider.URL,
					}

					// add Provider as a label
					manifest.ObjectMeta.Labels["provider"] = manifest.Status.Provider.Name
					manifest.ObjectMeta.Labels["provider-url"] = manifest.Status.Provider.URL
				}
			}

			// set CatalogSource labels
			manifest.ObjectMeta.Labels["catalog"] = manifest.Status.CatalogSource
			manifest.ObjectMeta.Labels["catalog-namespace"] = manifest.Status.CatalogSourceNamespace
			for k, v := range catsrc.GetLabels() {
				manifest.ObjectMeta.Labels[k] = v
			}

			logger.Debugf("retrieved packagemanifest %s", manifest.GetName())
			manifests = append(manifests, manifest)
		}
	}

	if !found {
		logger.Debug("ERROR: No valid resource found")
		return nil, fmt.Errorf("error parsing ConfigMap %s: no valid resources found", cmName)
	}

	return manifests, nil
}

func (m *InMemoryProvider) syncCatalogSource(obj interface{}) error {
	// assert as CatalogSource
	catsrc, ok := obj.(*operatorsv1alpha1.CatalogSource)
	if !ok {
		logrus.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting catalog source failed")
	}

	logger := logrus.WithFields(logrus.Fields{
		"Action": "Sync CatalogSource",
		"name":   catsrc.GetName(),
		"namespace": catsrc.GetNamespace(),
	})

	var manifests []packagev1alpha1.PackageManifest

	// handle by sourceType
	switch catsrc.Spec.SourceType {
	case "internal":
		// get the CatalogSource's ConfigMap
		cm, err := m.OpClient.KubernetesInterface().CoreV1().ConfigMaps(catsrc.GetNamespace()).Get(catsrc.Spec.ConfigMap, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get catalog config map %s when updating status: %s", catsrc.Spec.ConfigMap, err)
		}

		// parse PackageManifest from ConfigMap
		manifests, err = parsePackageManifestsFromConfigMap(cm, catsrc)
		if err != nil {
			return fmt.Errorf("failed to load package manifest from config map %s", cm.GetName())
		}

	default:
		return fmt.Errorf("catalog source %s in namespace %s source type %s not recognized", catsrc.GetName(), catsrc.GetNamespace(), catsrc.Spec.SourceType)
	}

	logger.Debug("updating in-memory PackageManifests")
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, manifest := range manifests {
		key := packageKey{
			catalogSourceName:      manifest.Status.CatalogSource,
			catalogSourceNamespace: manifest.Status.CatalogSourceNamespace,
			packageName:            manifest.GetName(),
		}

		if pm, ok := m.manifests[key]; ok {
			logger.Debugf("package %s already exists", key.packageName)
			manifest.CreationTimestamp = pm.ObjectMeta.CreationTimestamp
		} else {
			logger.Debugf("new package %s found", key.packageName)
			manifest.CreationTimestamp = metav1.NewTime(time.Now())
			for _, add := range m.add {
				if add.namespace == manifest.Status.CatalogSourceNamespace || add.namespace == metav1.NamespaceAll || manifest.Status.CatalogSourceNamespace == m.globalNamespace {
					logger.Debugf("sending new package %s to watcher for namespace %s", key.packageName, add.namespace)
					add.ch <- manifest
				}
			}
		}
		m.manifests[key] = manifest
	}

	return nil
}

func (m *InMemoryProvider) Get(namespace, name string) (*packagev1alpha1.PackageManifest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for key, pm := range m.manifests {
		if key.packageName == name && (key.catalogSourceNamespace == namespace || key.catalogSourceNamespace == m.globalNamespace) {
			pm.SetNamespace(namespace)
			return &pm, nil
		}
	}

	return nil, nil
}

func (m *InMemoryProvider) List(namespace string) (*packagev1alpha1.PackageManifestList, error) {
	manifestList := &packagev1alpha1.PackageManifestList{}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.manifests) > 0 {
		var matching []packagev1alpha1.PackageManifest
		for key, pm := range m.manifests {
			if namespace == metav1.NamespaceAll || key.catalogSourceNamespace == namespace || key.catalogSourceNamespace == m.globalNamespace {
				if namespace != metav1.NamespaceAll && pm.GetNamespace() != namespace {
					pm.SetNamespace(namespace)
				}
				matching = append(matching, pm)
			}
		}
		manifestList.Items = matching
	}
	return manifestList, nil
}

func (m *InMemoryProvider) Subscribe(namespace string, stopCh <-chan struct{}) (PackageChan, PackageChan, PackageChan, error) {
	logger := logrus.WithFields(logrus.Fields{
		"Action":    "PackageManifest Subscribe",
		"namespace": namespace,
	})

	m.mu.Lock()
	defer m.mu.Unlock()

	addEvent := eventChan{namespace, make(chan packagev1alpha1.PackageManifest)}
	modifyEvent := eventChan{namespace, make(chan packagev1alpha1.PackageManifest)}
	deleteEvent := eventChan{namespace, make(chan packagev1alpha1.PackageManifest)}
	m.add = append(m.add, addEvent)
	m.modify = append(m.modify, modifyEvent)
	m.delete = append(m.delete, deleteEvent)

	removeChan := func(target chan packagev1alpha1.PackageManifest, all []eventChan) []eventChan {
		for i, event := range all {
			if event.ch == target {
				logger.Debugf("closing channel")
				close(event.ch)
				return append(all[:i], all[i+1:]...)
			}
		}
		return all
	}

	go func() {
		<-stopCh
		m.mu.Lock()
		defer m.mu.Unlock()

		m.add = removeChan(addEvent.ch, m.add)
		m.modify = removeChan(modifyEvent.ch, m.modify)
		m.delete = removeChan(deleteEvent.ch, m.delete)
		return
	}()

	return addEvent.ch, modifyEvent.ch, deleteEvent.ch, nil
}
