package sqlite

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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

	for _, crd := range parsedCRDList {
		if crd.Spec.Versions == nil && crd.Spec.Version != "" {
			crd.Spec.Versions = []v1beta1.CustomResourceDefinitionVersion{{Name: crd.Spec.Version, Served: true, Storage: true}}
		}
		for _, version := range crd.Spec.Versions {
			gvk := registry.APIKey{Group: crd.Spec.Group, Version: version.Name, Kind: crd.Spec.Names.Kind, Plural: crd.Spec.Names.Plural}
			c.log.WithField("gvk", gvk).Debug("loading CRD")
			if _, ok := c.crds[gvk]; ok {
				c.log.WithField("gvk", gvk).Debug("crd added twice")
				return fmt.Errorf("can't add the same CRD twice in one configmap")
			}
			crdUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&crd)
			if err != nil {
				c.log.WithError(err).Debug("error remarshalling crd")
				return err
			}
			c.crds[gvk] = &unstructured.Unstructured{Object: crdUnst}
		}
	}

	c.log.Info("loading Bundles")
	csvListYaml, ok := c.configMapData[ConfigMapCSVName]
	if !ok {
		return fmt.Errorf("couldn't find expected key %s in configmap", ConfigMapCSVName)
	}
	csvListJson, err := yaml.YAMLToJSON([]byte(csvListYaml))
	if err != nil {
		c.log.WithError(err).Debug("error loading CSV list")
		return err
	}

	var parsedCSVList []v1alpha1.ClusterServiceVersion
	err = json.Unmarshal([]byte(csvListJson), &parsedCSVList)
	if err != nil {
		c.log.WithError(err).Debug("error parsing CSV list")
		return err
	}

	for _, csv := range parsedCSVList {
		c.log.WithField("csv", csv.Name).Debug("loading CSV")
		csvUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&csv)
		if err != nil {
			c.log.WithError(err).Debug("error remarshalling csv")
			return err
		}

		bundle := registry.NewBundle(csv.GetName(), "", "", &unstructured.Unstructured{Object: csvUnst})
		for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
			split := strings.SplitN(owned.Name, ".", 2)
			if len(split) < 2 {
				c.log.WithError(err).Debug("error parsing owned name")
				return fmt.Errorf("error parsing owned name")
			}
			gvk := registry.APIKey{Group: split[1], Version: owned.Version, Kind: owned.Kind, Plural: split[0]}
			if crdUnst, ok := c.crds[gvk]; !ok {
				c.log.WithField("gvk", gvk).WithError(err).Warn("couldn't find owned CRD in crd list")
			} else {
				bundle.Add(crdUnst)
			}
		}
		if err := c.store.AddOperatorBundle(bundle); err != nil {
			return err
		}
	}

	c.log.Info("loading Packages")
	packageListYaml, ok := c.configMapData[ConfigMapPackageName]
	if !ok {
		return fmt.Errorf("couldn't find expected key %s in configmap", ConfigMapPackageName)
	}

	packageListJson, err := yaml.YAMLToJSON([]byte(packageListYaml))
	if err != nil {
		c.log.WithError(err).Debug("error loading package list")
		return err
	}

	var parsedPackageManifests []registry.PackageManifest
	err = json.Unmarshal([]byte(packageListJson), &parsedPackageManifests)
	if err != nil {
		c.log.WithError(err).Debug("error parsing package list")
		return err
	}
	for _, packageManifest := range parsedPackageManifests {
		c.log.WithField("package", packageManifest.PackageName).Debug("loading package")
		if err := c.store.AddPackageChannels(packageManifest); err != nil {
			return err
		}
	}

	c.log.Info("extracting provided API information")
	if err := c.store.AddProvidedAPIs(); err != nil {
		return err
	}
	return nil
}
