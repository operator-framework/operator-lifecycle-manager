package catalog

import (
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
)

// Catalog Source
//    - Map name to ClusterServiceVersion
//    - Map CRD to CRD definition
//    - Map CRD to ClusterServiceVersion that manages it

type Source interface {
	FindCSVForPackageNameUnderChannel(packageName string, channelName string) (*v1alpha1.ClusterServiceVersion, error)
	FindReplacementCSVForPackageNameUnderChannel(packageName string, channelName string, csvName string) (*v1alpha1.ClusterServiceVersion, error)

	// Deprecated: Switch to FindReplacementCSVForPackageNameUnderChannel when the caller has package and channel
	// information.
	FindReplacementCSVForName(name string) (*v1alpha1.ClusterServiceVersion, error)

	FindCSVByName(name string) (*v1alpha1.ClusterServiceVersion, error)
	ListServices() ([]v1alpha1.ClusterServiceVersion, error)

	FindCRDByKey(key CRDKey) (*v1beta1.CustomResourceDefinition, error)
	FindLatestCSVForCRD(key CRDKey) (*v1alpha1.ClusterServiceVersion, error)
	ListCSVsForCRD(key CRDKey) ([]v1alpha1.ClusterServiceVersion, error)
}

// CSVMetadata holds the necessary information to locate a particular CSV in the catalog
type CSVMetadata struct {
	Name    string
	Version string
}

// PackageManifest holds information about a package, which is a reference to one (or more)
// channels under a single package.
type PackageManifest struct {
	// PackageName is the name of the overall package, ala `etcd`.
	PackageName string `json:"packageName"`

	// Channels are the declared channels for the package, ala `stable` or `alpha`.
	Channels []PackageChannel `json:"channels"`
}

// PackageChannel defines a single channel under a package, pointing to a version of that
// package.
type PackageChannel struct {
	// Name is the name of the channel, e.g. `alpha` or `stable`
	Name string `json:"name"`

	// CurrentCSVName defines a reference to the CSV holding the version of this package currently
	// for the channel.
	CurrentCSVName string `json:"currentCSV"`
}
