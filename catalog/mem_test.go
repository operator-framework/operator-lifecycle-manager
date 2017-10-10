package catalog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

func TestFindClusterServiceVersionByServiceName(t *testing.T) {
	catalog := NewInMem()
	csv1 := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionCRDName,
			APIVersion: v1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "MockCSV",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{},
	}
	crd1 := apiextensions.CustomResourceDefinition{}
	crd1.SetName("MyCRD1")
	crds := []apiextensions.CustomResourceDefinition{crd1}
	catalog.addService(csv1, crds)

	foundCSV, err := catalog.FindClusterServiceVersionByServiceName("MockCSV")
	assert.NoError(t, err)
	assert.Equal(t, "MockCSV", foundCSV.GetName())
}
