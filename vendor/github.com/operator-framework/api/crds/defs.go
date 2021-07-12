package crds

// Generate embedded files from CRDs to avoid file path changes when this package is imported.
//go:generate go run github.com/go-bindata/go-bindata/v3/go-bindata -pkg crds -o zz_defs.go -ignore=.*\.go -nometadata .

import (
	"bytes"
	"fmt"
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// crdFile is a descriptor of a file containing a CustomResourceDefinition.
type crdFile string

// path returns the path of the file.
func (c crdFile) path() string {
	s := string(c)
	return s
}

// mustUnmarshal unmarshals the file into a CRD and panics on failure.
func (c crdFile) mustUnmarshal() *apiextensionsv1.CustomResourceDefinition {
	path := c.path()
	data, err := Asset(path)
	if err != nil {
		panic(fmt.Errorf("unable to read crd file %s: %s", path, err))
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}
	reader := bytes.NewReader(data)
	decoder := yaml.NewYAMLOrJSONDecoder(reader, 30)
	if err = decoder.Decode(crd); err != nil {
		panic(fmt.Errorf("failed to unmarshal to crd:  %s", err))
	}

	if gvk := crd.GroupVersionKind(); gvk != supportedGVK {
		panic(fmt.Errorf("%s not supported", gvk))
	}

	return crd
}

var (
	lock sync.Mutex

	// loaded stores previously unmarshaled CustomResourceDefinitions indexed by their file descriptor.
	loaded = map[crdFile]*apiextensionsv1.CustomResourceDefinition{}
	// supportedGVK is the version of CustomResourceDefinition supported for unmarshaling.
	supportedGVK = apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition")
)

// getCRD lazily loads and returns the CustomResourceDefinition unmarshaled from a file.
func getCRD(file crdFile) *apiextensionsv1.CustomResourceDefinition {
	lock.Lock()
	defer lock.Unlock()

	if crd, ok := loaded[file]; ok && crd != nil {
		return crd
	}

	// Unmarshal and memoize
	crd := file.mustUnmarshal()
	loaded[file] = crd

	return crd
}

// TODO(njhale): codegen this.

// CatalogSource returns a copy of the CustomResourceDefinition for the latest version of the CatalogSource API.
func CatalogSource() *apiextensionsv1.CustomResourceDefinition {
	return getCRD("operators.coreos.com_catalogsources.yaml").DeepCopy()
}

// ClusterServiceVersion returns a copy of the CustomResourceDefinition for the latest version of the ClusterServiceVersion API.
func ClusterServiceVersion() *apiextensionsv1.CustomResourceDefinition {
	return getCRD("operators.coreos.com_clusterserviceversions.yaml").DeepCopy()
}

// InstallPlan returns a copy of the CustomResourceDefinition for the latest version of the InstallPlan API.
func InstallPlan() *apiextensionsv1.CustomResourceDefinition {
	return getCRD("operators.coreos.com_installplans.yaml").DeepCopy()
}

// OperatorGroup returns a copy of the CustomResourceDefinition for the latest version of the OperatorGroup API.
func OperatorGroup() *apiextensionsv1.CustomResourceDefinition {
	return getCRD("operators.coreos.com_operatorgroups.yaml").DeepCopy()
}

// Operator returns a copy of the CustomResourceDefinition for the latest version of the Operator API.
func Operator() *apiextensionsv1.CustomResourceDefinition {
	return getCRD("operators.coreos.com_operators.yaml").DeepCopy()
}

// Subscription returns a copy of the CustomResourceDefinition for the latest version of the Subscription API.
func Subscription() *apiextensionsv1.CustomResourceDefinition {
	return getCRD("operators.coreos.com_subscriptions.yaml").DeepCopy()
}

// OperatorCondition returns a copy of the CustomResourceDefinition for the latest version of the OperatorCondition API.
func OperatorCondition() *apiextensionsv1.CustomResourceDefinition {
	return getCRD("operators.coreos.com_operatorconditions.yaml").DeepCopy()
}
