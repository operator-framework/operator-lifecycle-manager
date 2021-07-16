package sqlite

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

const (
	ConfigMapCRDName     = "customResourceDefinitions"
	ConfigMapCSVName     = "clusterServiceVersions"
	ConfigMapPackageName = "packages"
)

// ConfigMapLoader loads a configmap of resources into the database
// entries under "customResourceDefinitions" will be parsed as CRDs
// entries under "clusterServiceVersions"  will be parsed as CSVs
// entries under "packages" will be parsed as Packages
type ConfigMapLoader struct {
	log           *logrus.Entry
	store         registry.Load
	configMapData map[string]string
	crds          map[registry.APIKey]*unstructured.Unstructured
}

var _ SQLPopulator = &ConfigMapLoader{}

// NewSQLLoaderForConfigMapData is useful when the operator manifest(s)
// originate from a different source than a configMap. For example, operator
// manifest(s) can be downloaded from a remote registry like quay.io.
func NewSQLLoaderForConfigMapData(logger *logrus.Entry, store registry.Load, configMapData map[string]string) *ConfigMapLoader {
	return &ConfigMapLoader{
		log:           logger,
		store:         store,
		configMapData: configMapData,
		crds:          map[registry.APIKey]*unstructured.Unstructured{},
	}
}

func NewSQLLoaderForConfigMap(store registry.Load, configMap v1.ConfigMap) *ConfigMapLoader {
	logger := logrus.WithFields(logrus.Fields{"configmap": configMap.GetName(), "ns": configMap.GetNamespace()})
	return &ConfigMapLoader{
		log:           logger,
		store:         store,
		configMapData: configMap.Data,
		crds:          map[registry.APIKey]*unstructured.Unstructured{},
	}
}

func (c *ConfigMapLoader) Populate() error {
	c.log.Info("loading CRDs")

	// first load CRDs into memory; these will be added to the bundle that owns them
	crdListYaml, ok := c.configMapData[ConfigMapCRDName]
	if !ok {
		return fmt.Errorf("couldn't find expected key %s in configmap", ConfigMapCRDName)
	}

	crdListJson, err := yaml.YAMLToJSON([]byte(crdListYaml))
	if err != nil {
		c.log.WithError(err).Debug("error loading CRD list")
		return err
	}

	var parsedCRDList []v1beta1.CustomResourceDefinition
	if err := json.Unmarshal(crdListJson, &parsedCRDList); err != nil {
		c.log.WithError(err).Debug("error parsing CRD list")
		return err
	}

	var errs []error
	for _, crd := range parsedCRDList {
		if crd.Spec.Versions == nil && crd.Spec.Version != "" {
			crd.Spec.Versions = []v1beta1.CustomResourceDefinitionVersion{{Name: crd.Spec.Version, Served: true, Storage: true}}
		}
		for _, version := range crd.Spec.Versions {
			gvk := registry.APIKey{Group: crd.Spec.Group, Version: version.Name, Kind: crd.Spec.Names.Kind, Plural: crd.Spec.Names.Plural}
			c.log.WithField("gvk", gvk).Debug("loading CRD")
			if _, ok := c.crds[gvk]; ok {
				c.log.WithField("gvk", gvk).Debug("crd added twice")
				errs = append(errs, fmt.Errorf("can't add the same CRD twice in one configmap: %v", gvk))
				continue
			}
			crdUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&crd)
			if err != nil {
				errs = append(errs, fmt.Errorf("error marshaling crd: %s", err))
				continue
			}
			c.crds[gvk] = &unstructured.Unstructured{Object: crdUnst}
		}
	}

	c.log.Info("loading Bundles")
	csvListYaml, ok := c.configMapData[ConfigMapCSVName]
	if !ok {
		errs = append(errs, fmt.Errorf("couldn't find expected key %s in configmap", ConfigMapCSVName))
		return utilerrors.NewAggregate(errs)
	}
	csvListJson, err := yaml.YAMLToJSON([]byte(csvListYaml))
	if err != nil {
		errs = append(errs, fmt.Errorf("error loading CSV list: %s", err))
		return utilerrors.NewAggregate(errs)
	}

	var parsedCSVList []registry.ClusterServiceVersion
	err = json.Unmarshal(csvListJson, &parsedCSVList)
	if err != nil {
		errs = append(errs, fmt.Errorf("error parsing CSV list: %s", err))
		return utilerrors.NewAggregate(errs)
	}

	for _, csv := range parsedCSVList {
		c.log.WithField("csv", csv.GetName()).Debug("loading CSV")
		csvUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&csv)
		if err != nil {
			errs = append(errs, fmt.Errorf("error marshaling csv: %s", err))
			continue
		}

		bundle := registry.NewBundle(csv.GetName(), &registry.Annotations{}, &unstructured.Unstructured{Object: csvUnst})
		ownedCRDs, _, err := csv.GetCustomResourceDefintions()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, owned := range ownedCRDs {
			split := strings.SplitN(owned.Name, ".", 2)
			if len(split) < 2 {
				c.log.WithError(err).Debug("error parsing owned name")
				errs = append(errs, fmt.Errorf("error parsing owned name: %s", err))
				continue
			}

			gvk := registry.APIKey{Group: split[1], Version: owned.Version, Kind: owned.Kind, Plural: split[0]}
			crdUnst, ok := c.crds[gvk]
			if !ok {
				errs = append(errs, fmt.Errorf("couldn't find owned CRD in crd list %v: %s", gvk, err))
				continue
			}

			bundle.Add(crdUnst)
		}

		if err := c.store.AddOperatorBundle(bundle); err != nil {
			version, _ := bundle.Version()
			errs = append(errs, fmt.Errorf("error adding operator bundle %s/%s/%s: %s", csv.GetName(), version, bundle.BundleImage, err))
		}
	}

	c.log.Info("loading Packages")
	packageListYaml, ok := c.configMapData[ConfigMapPackageName]
	if !ok {
		errs = append(errs, fmt.Errorf("couldn't find expected key %s in configmap", ConfigMapPackageName))
		return utilerrors.NewAggregate(errs)
	}

	packageListJson, err := yaml.YAMLToJSON([]byte(packageListYaml))
	if err != nil {
		errs = append(errs, fmt.Errorf("error loading package list: %s", err))
		return utilerrors.NewAggregate(errs)
	}

	var parsedPackageManifests []registry.PackageManifest
	err = json.Unmarshal(packageListJson, &parsedPackageManifests)
	if err != nil {
		errs = append(errs, fmt.Errorf("error parsing package list: %s", err))
		return utilerrors.NewAggregate(errs)
	}
	for _, packageManifest := range parsedPackageManifests {
		c.log.WithField("package", packageManifest.PackageName).Debug("loading package")
		if err := c.store.AddPackageChannels(packageManifest); err != nil {
			errs = append(errs, fmt.Errorf("error loading package %s: %s", packageManifest.PackageName, err))
		}
	}

	return utilerrors.NewAggregate(errs)
}
