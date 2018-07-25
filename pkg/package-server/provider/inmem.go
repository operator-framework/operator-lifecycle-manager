package provider

import (
	"encoding/json"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/ghodss/yaml"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	packagev1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
	"github.com/prometheus/common/log"
)

const ConfigMapPackageName = "packages"

type packageKey struct {
	catalogSourceName      string
	catalogSourceNamespace string
	packageName            string
}

type InMemoryProvider struct {
	*queueinformer.Operator
	// catalogSourceLister catalogv1alpha1.CatalogSourceLister

	mu        sync.RWMutex
	manifests map[packageKey]packagev1alpha1.PackageManifest
}

func NewInMemoryProvider(informers []cache.SharedIndexInformer, queueOperator *queueinformer.Operator) *InMemoryProvider {
	// instantiate the in-mem provider
	prov := &InMemoryProvider{
		Operator:  queueOperator,
		manifests: make(map[packageKey]packagev1alpha1.PackageManifest),
	}

	// register CatalogSource informers.
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "catalogsources")
	queueInformers := queueinformer.New(
		queue,
		informers,
		prov.syncCatalogSources,
		nil,
		"catsrc",
	)
	for _, informer := range queueInformers {
		prov.RegisterQueueInformer(informer)
	}

	return prov
}

func loadPackageManifestsFromConfigMap(cm *corev1.ConfigMap) ([]packagev1alpha1.PackageManifest, error) {
	var manifests []packagev1alpha1.PackageManifest

	cmName := cm.GetName()
	found := false
	packageListYaml, ok := cm.Data[ConfigMapPackageName]
	if ok {
		log.Debug("Load ConfigMap      -- ConfigMap contains packages")
		packageListJson, err := yaml.YAMLToJSON([]byte(packageListYaml))
		if err != nil {
			log.Debugf("Load ConfigMap     -- ERROR %s : error=%s", cmName, err)
			return nil, fmt.Errorf("error loading package list yaml from ConfigMap %s: %s", cmName, err)
		}

		var parsedSpecs []packagev1alpha1.PackageManifestSpec
		err = json.Unmarshal([]byte(packageListJson), &parsedSpecs)
		if err != nil {
			log.Debugf("Load ConfigMap     -- ERROR %s : error=%s", cmName, err)
			return nil, fmt.Errorf("error parsing package list (json) from ConfigMap %s: %s", cmName, err)
		}

		for _, spec := range parsedSpecs {
			found = true
			manifest := packagev1alpha1.PackageManifest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      spec.PackageName,
					Namespace: cm.GetNamespace(),
				},
				Spec: spec,
			}
			// TODO: add check for invalid package definitions
			manifests = append(manifests, manifest)
		}
		log.Debugf("Load ConfigMap      -- Found packages: %v", manifests)
	}

	if !found {
		log.Debugf("Load ConfigMap     -- ERROR %s : no resources found", cmName)
		return nil, fmt.Errorf("error parsing ConfigMap %s: no valid resources found", cmName)
	}

	return manifests, nil
}

func (m *InMemoryProvider) syncCatalogSources(obj interface{}) error {
	// assert as catalog source
	catsrc, ok := obj.(*operatorsv1alpha1.CatalogSource)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting catalog source failed")
	}

	var manifests []packagev1alpha1.PackageManifest

	// check if the sourcetype is internal
	switch catsrc.Spec.SourceType {
	case "internal":
		// get the catalog source's config map
		cm, err := m.OpClient.KubernetesInterface().CoreV1().ConfigMaps(catsrc.GetNamespace()).Get(catsrc.Spec.ConfigMap, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get catalog config map %s when updating status: %s", catsrc.Spec.ConfigMap, err)
		}

		// load the package manifest from the config map
		manifests, err = loadPackageManifestsFromConfigMap(cm)
		if err != nil {
			return fmt.Errorf("failed to load package manifest from config map %s", cm.GetName())
		}

	default:
		return fmt.Errorf("catalog source %s in namespace %s source type %s not recognized", catsrc.GetName(), catsrc.GetNamespace(), catsrc.Spec.SourceType)
	}

	// update package manifests
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, manifest := range manifests {
		key := packageKey{
			catalogSourceName:      catsrc.GetName(),
			catalogSourceNamespace: catsrc.GetNamespace(),
			packageName:            manifest.Spec.PackageName,
		}
		m.manifests[key] = manifest
	}

	return nil
}

// ListPackageManifests implements PackageManifestProvider.ListPackageManifests()
func (m *InMemoryProvider) ListPackageManifests(namespace string) (*packagev1alpha1.PackageManifestList, error) {
	manifestList := &packagev1alpha1.PackageManifestList{}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.manifests) > 0 {
		var matching []packagev1alpha1.PackageManifest
		for _, manifest := range m.manifests {
			if manifest.GetNamespace() == namespace {
				matching = append(matching, manifest)
			}
		}

		manifestList.Items = matching
	}

	return manifestList, nil
}

// GetPackageManifest implements PackageManifestProvider.GetPackageManifest(...)
func (m *InMemoryProvider) GetPackageManifest(namespace, name string) (*packagev1alpha1.PackageManifest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	manifest := &packagev1alpha1.PackageManifest{}
	for key, pm := range m.manifests {
		if key.packageName == name && key.catalogSourceNamespace == namespace {
			manifest = &pm
		}
	}

	return manifest, nil
}
