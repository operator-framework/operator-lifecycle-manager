package catalog

import (
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/assert"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

// If there are multiple versions of a CSV, FindClusterServiceVersionByServiceName gets the latest one
// If there are multiple versions of a CSV, FindClusterServiceVersionByReplaces should be able to retrieve any of them (according to replaces field value)
// If I query for a crd by name, I get a crd that I can deserialize into a thing I kubernetes recognizes as a real CRD.
// We can make multiple queries for different services and get the right CSVs out.
// A full dependency test, where we can get a CSV by service name, read it's crd requirements, get its CRDs, and for each of them, get the corresponding owner CSV.

func TestFindClusterServiceVersionByServiceName(t *testing.T) {
	catalog := NewInMem()

	crd1 := v1beta1.CustomResourceDefinition{}
	crd1.SetName("MyCRD1")
	catalog.SetOrReplaceCRDDefinition(crd1)

	csv1 := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionCRDName,
			APIVersion: v1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "MockCSV",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces: "",
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    []string{"MyCRD1"},
				Required: []string{},
			},
		},
	}

	catalog.AddOrReplaceService(csv1)

	foundCSV, err := catalog.FindLatestCSVByServiceName("MockCSV")
	assert.NoError(t, err)
	assert.NotNil(t, foundCSV)
	assert.Equal(t, "MockCSV", foundCSV.GetName())
}

func TestFindClusterServiceVersionByServiceNameAndVersion(t *testing.T) {
	var (
		testCSVName    = "MockServiceName-v1"
		testCSVVersion = "0.2.4+alpha"
		testCRDName    = "MockServiceResource-v2"
	)

	testCSVResource := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionCRDName,
			APIVersion: v1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: testCSVName,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Version: *semver.New(testCSVVersion),
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    []string{testCRDName},
				Required: []string{},
			},
		},
	}

	testCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testCRDName,
		},
	}

	catalog := NewInMem()
	catalog.SetOrReplaceCRDDefinition(testCRDDefinition)
	catalog.AddOrReplaceService(testCSVResource)

	foundCSV, err := catalog.FindLatestCSVByServiceName(testCSVName)
	assert.NoError(t, err)
	assert.Equal(t, testCSVName, foundCSV.GetName())
	assert.Equal(t, testCSVVersion, foundCSV.Spec.Version.String())
}

func TestListCSVsForServiceName(t *testing.T) {
	var (
		testCSVName     = "MockServiceName-v1"
		testCSVVersion1 = "0.2.4+alpha"
		testCSVVersion2 = "1.0.1"

		testCRDName = "MockServiceResource-v2"
	)

	testCSVResource1 := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionCRDName,
			APIVersion: v1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: testCSVName,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Version: *semver.New(testCSVVersion1),
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    []string{testCRDName},
				Required: []string{},
			},
		},
	}

	testCSVResource2 := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionCRDName,
			APIVersion: v1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: testCSVName,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Version: *semver.New(testCSVVersion2),
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    []string{testCRDName},
				Required: []string{},
			},
		},
	}

	testCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testCRDName,
		},
	}

	catalog := NewInMem()
	catalog.SetOrReplaceCRDDefinition(testCRDDefinition)
	catalog.AddOrReplaceService(testCSVResource1)
	catalog.AddOrReplaceService(testCSVResource2)

	csvs, err := catalog.ListCSVsForServiceName(testCSVName)

	assert.NoError(t, err)
	assert.Equal(t, 2, len(csvs))
	assert.Equal(t, testCSVName, csvs[0].GetName())
	assert.Equal(t, testCSVName, csvs[1].GetName())
	assert.Equal(t, testCSVVersion1, csvs[0].Spec.Version.String())
	assert.Equal(t, testCSVVersion2, csvs[1].Spec.Version.String())
}
