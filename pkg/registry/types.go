//go:generate counterfeiter types.go Source

package registry

import (
	"fmt"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	uiv1alpha1 "github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
)

// Catalog Source
//    - Map name to ClusterServiceVersion
//    - Map CRD to CRD definition
//    - Map CRD to ClusterServiceVersion that manages it

type Source interface {
	FindCSVForPackageNameUnderChannel(packageName string, channelName string) (*v1alpha1.ClusterServiceVersion, error)
	FindReplacementCSVForPackageNameUnderChannel(packageName string, channelName string, csvName string) (*v1alpha1.ClusterServiceVersion, error)
	AllPackages() map[string]uiv1alpha1.PackageManifest

	// Deprecated: Switch to FindReplacementCSVForPackageNameUnderChannel when the caller has package and channel
	// information.
	FindReplacementCSVForName(name string) (*v1alpha1.ClusterServiceVersion, error)

	FindCSVByName(name string) (*v1alpha1.ClusterServiceVersion, error)
	ListServices() ([]v1alpha1.ClusterServiceVersion, error)

	FindCRDByKey(key CRDKey) (*v1beta1.CustomResourceDefinition, error)
	ListLatestCSVsForCRD(key CRDKey) ([]CSVAndChannelInfo, error)
}

// CRDKey contains metadata needed to uniquely identify a CRD
type CRDKey struct {
	Kind    string
	Name    string
	Version string
}

func (k CRDKey) String() string {
	return fmt.Sprintf("%s/%s/%s", k.Kind, k.Name, k.Version)
}

// CSVAndChannelInfo holds information about a CSV and the channel in which it lives.
type CSVAndChannelInfo struct {
	// CSV is the CSV found.
	CSV *v1alpha1.ClusterServiceVersion

	// Channel is the channel that "contains" this CSV, as it is declared as part of the channel.
	Channel uiv1alpha1.PackageChannel

	// IsDefaultChannel returns true iff the channel is the default channel for the package.
	IsDefaultChannel bool
}

// CSVMetadata holds the necessary information to locate a particular CSV in the catalog
type CSVMetadata struct {
	Name    string
	Version string
}
