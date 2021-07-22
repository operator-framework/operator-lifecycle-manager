package provider

import (
	"k8s.io/apimachinery/pkg/labels"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/available-csvs/apis/available"
)

type Interface interface {
	Get(namespace, name string) (*available.AvailableClusterServiceVersion, error)
	List(namespace string, selector labels.Selector) (*available.AvailableClusterServiceVersionList, error)
}
