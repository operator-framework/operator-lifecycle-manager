package catalog

import (
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/assert"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

// compareResources compares resource equality then prints a diff for easier debugging
func compareResources(t *testing.T, expected, actual interface{}) {
	if eq := equality.Semantic.DeepEqual(expected, actual); !eq {
		t.Fatalf("ClusterServiceVerson does not match expected value: %s",
			diff.ObjectDiff(expected, actual))
	}
}

func createCSV(name, version, replaces string, owned []string) v1alpha1.ClusterServiceVersion {
	return v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionCRDName,
			APIVersion: v1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Version:  *semver.New(version),
			Replaces: replaces,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    owned,
				Required: []string{},
			},
		},
	}
}

// If there are multiple versions of a CSV, FindClusterServiceVersionByServiceName gets the latest one
// If there are multiple versions of a CSV, FindClusterServiceVersionByReplaces should be able to retrieve any of them (according to replaces field value)
// If I query for a crd by name, I get a crd that I can deserialize into a thing I kubernetes recognizes as a real CRD.
// We can make multiple queries for different services and get the right CSVs out.
// A full dependency test, where we can get a CSV by service name, read it's crd requirements, get its CRDs, and for each of them, get the corresponding owner CSV.

func TestFindClusterServiceVersionByServiceNameAndVersion(t *testing.T) {
	var (
		testCSVName    = "MockServiceName-v1"
		testCSVVersion = "0.2.4+alpha"
		testCRDName    = "MockServiceResource-v2"
	)

	testCSVResource := createCSV(testCSVName, testCSVVersion, "", []string{testCRDName})

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
	compareResources(t, &testCSVResource, foundCSV)
}

func TestFindReplacementByServiceName(t *testing.T) {
	var (
		testCSVName = "mockservice-operator.stable"

		testCSVAlphaVersion  = "0.2.4+alpha"
		testCSVPriorVersion  = "0.2.4"
		testCSVLatestVersion = "1.0.1"

		testOwnedCRDName = "mockserviceresource-v1.catalog.testing.coreos.com"
		testOtherCRDName = "mockrandomresource-v1.catalog.testing.coreos.com"

		testReplacesName = "mockservice-operator.prealpha"
	)

	testCSVResourceAlpha := createCSV(testCSVName, testCSVAlphaVersion,
		testReplacesName, []string{testOwnedCRDName})

	testCSVResourcePrior := createCSV(testCSVName, testCSVPriorVersion,
		testReplacesName, []string{testOwnedCRDName})

	testCSVResourceLatest := createCSV(testCSVName, testCSVLatestVersion,
		testReplacesName, []string{testOwnedCRDName})

	otherTestCSVResource := createCSV("notmockservice.1", "1.2.3", "", []string{testOtherCRDName})

	testOwnedCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testOwnedCRDName,
		},
	}

	testOtherCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testOtherCRDName,
		},
	}

	catalog := NewInMem()
	catalog.SetOrReplaceCRDDefinition(testOwnedCRDDefinition)
	catalog.SetOrReplaceCRDDefinition(testOtherCRDDefinition)

	catalog.AddOrReplaceService(testCSVResourceAlpha)
	catalog.AddOrReplaceService(testCSVResourcePrior)
	catalog.AddOrReplaceService(testCSVResourceLatest)
	catalog.AddOrReplaceService(otherTestCSVResource)

	foundCSV, err := catalog.FindReplacementForServiceName(testReplacesName)
	assert.NoError(t, err)
	assert.Equal(t, testCSVName, foundCSV.GetName())
	assert.Equal(t, testCSVLatestVersion, foundCSV.Spec.Version.String(),
		"did not get latest version of CSV")
	compareResources(t, &testCSVResourceLatest, foundCSV)
}

func TestListCSVsForServiceName(t *testing.T) {
	var (
		testCSVName     = "MockServiceName-v1"
		testCSVVersion2 = "0.2.4+alpha"
		testCSVVersion1 = "1.0.1"

		testCRDName = "MockServiceResource-v2"
	)

	testCSVResource1 := createCSV(testCSVName, testCSVVersion1, "", []string{testCRDName})

	testCSVResource2 := createCSV(testCSVName, testCSVVersion2, "", []string{testCRDName})

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
	compareResources(t, testCSVResource1, csvs[0])
	compareResources(t, testCSVResource2, csvs[1])
}
