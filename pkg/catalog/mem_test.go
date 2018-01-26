package catalog

import (
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/assert"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	uiv1alpha1 "github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
)

// compareResources compares resource equality then prints a diff for easier debugging
func compareResources(t *testing.T, expected, actual interface{}) {
	if eq := equality.Semantic.DeepEqual(expected, actual); !eq {
		t.Fatalf("ClusterServiceVerson does not match expected value: %s",
			diff.ObjectDiff(expected, actual))
	}
}

func createCSV(name, version, replaces string, owned []string) v1alpha1.ClusterServiceVersion {
	ownedResources := []v1alpha1.CRDDescription{}
	for _, ownedCRD := range owned {
		ownedResources = append(ownedResources, v1alpha1.CRDDescription{
			Name: ownedCRD,
		})
	}
	return v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionCRDName,
			APIVersion: v1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "alm-coreos-tests",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Version:  *semver.New(version),
			Replaces: replaces,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    ownedResources,
				Required: []v1alpha1.CRDDescription{},
			},
		},
	}
}

// If there are multiple versions of a CSV, FindClusterServiceVersionByName gets the latest one
// If there are multiple versions of a CSV, FindClusterServiceVersionByReplaces should be able to retrieve any of them (according to replaces field value)
// If I query for a crd by name, I get a crd that I can deserialize into a thing I kubernetes recognizes as a real CRD.
// We can make multiple queries for different services and get the right CSVs out.
// A full dependency test, where we can get a CSV by service name, read it's crd requirements, get its CRDs, and for each of them, get the corresponding owner CSV.

func TestFindClusterServiceVersionByNameAndVersion(t *testing.T) {
	var (
		testCSVName    = "MockName-v1"
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
	catalog.setOrReplaceCRDDefinition(testCRDDefinition)
	catalog.AddOrReplaceService(testCSVResource)

	foundCSV, err := catalog.FindCSVByName(testCSVName)
	assert.NoError(t, err)
	assert.Equal(t, testCSVName, foundCSV.GetName())
	assert.Equal(t, testCSVVersion, foundCSV.Spec.Version.String())
	compareResources(t, &testCSVResource, foundCSV)
}

func TestFindCSVForPackageNameUnderChannel(t *testing.T) {
	var (
		testCSVName = "mockservice-operator."

		testCSVAlphaVersion  = "0.2.4+alpha"
		testCSVStableVersion = "0.2.4"

		testOwnedCRDName = "mockserviceresource-v1.catalog.testing.coreos.com"
	)

	// Cluster has both alpha and stable running, with no replaces.
	testCSVResourceAlpha := createCSV(testCSVName+testCSVAlphaVersion, testCSVAlphaVersion,
		"", []string{testOwnedCRDName})

	testCSVResourceStable := createCSV(testCSVName+testCSVStableVersion, testCSVStableVersion,
		"", []string{testOwnedCRDName})

	testOwnedCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testOwnedCRDName,
		},
	}

	catalog := NewInMem()
	catalog.setOrReplaceCRDDefinition(testOwnedCRDDefinition)

	catalog.AddOrReplaceService(testCSVResourceAlpha)
	catalog.AddOrReplaceService(testCSVResourceStable)

	catalog.addPackageManifest(uiv1alpha1.PackageManifest{
		PackageName: "mockservice",
		Channels: []uiv1alpha1.PackageChannel{
			{
				Name:           "alpha",
				CurrentCSVName: testCSVName + testCSVAlphaVersion,
			},
			{
				Name:           "stable",
				CurrentCSVName: testCSVName + testCSVStableVersion,
			},
		},
	})

	alphaCSV, err := catalog.FindCSVForPackageNameUnderChannel("mockservice", "alpha")
	assert.NoError(t, err)
	assert.Equal(t, testCSVName+testCSVAlphaVersion, alphaCSV.GetName())
	compareResources(t, &testCSVResourceAlpha, alphaCSV)

	stableCSV, err := catalog.FindCSVForPackageNameUnderChannel("mockservice", "stable")
	assert.NoError(t, err)
	assert.Equal(t, testCSVName+testCSVStableVersion, stableCSV.GetName())
	compareResources(t, &testCSVResourceStable, stableCSV)

	_, err = catalog.FindCSVForPackageNameUnderChannel("mockservice", "invalid")
	assert.Error(t, err)

	_, err = catalog.FindCSVForPackageNameUnderChannel("weirdservice", "alpha")
	assert.Error(t, err)
}

func TestInvalidPackageManifest(t *testing.T) {
	catalog := NewInMem()

	err := catalog.addPackageManifest(uiv1alpha1.PackageManifest{
		PackageName: "mockservice",
		Channels: []uiv1alpha1.PackageChannel{
			{
				Name:           "alpha",
				CurrentCSVName: "somecsv",
			},
		},
	})

	assert.Error(t, err)
}

func TestFindReplacementCSVForName(t *testing.T) {
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
	catalog.setOrReplaceCRDDefinition(testOwnedCRDDefinition)
	catalog.setOrReplaceCRDDefinition(testOtherCRDDefinition)

	catalog.AddOrReplaceService(testCSVResourceAlpha)
	catalog.AddOrReplaceService(testCSVResourcePrior)
	catalog.AddOrReplaceService(testCSVResourceLatest)
	catalog.AddOrReplaceService(otherTestCSVResource)

	foundCSV, err := catalog.FindReplacementCSVForName(testReplacesName)
	assert.NoError(t, err)
	assert.Equal(t, testCSVName, foundCSV.GetName())
	assert.Equal(t, testCSVLatestVersion, foundCSV.Spec.Version.String(),
		"did not get latest version of CSV")
	compareResources(t, &testCSVResourceLatest, foundCSV)
}

func TestFindReplacementCSVForPackageNameUnderChannel(t *testing.T) {
	var (
		testStableCSVName   = "mockservice-operator.v1.0.0"
		testBetaCSVName     = "mockservice-operator.v1.1.0"
		testAlphaCSVName    = "mockservice-operator.v1.2.0"
		testReplacedCSVName = "mockservice-operator.v0.0.9"

		testCSVStableVersion   = "1.0.0"
		testCSVBetaVersion     = "1.1.0"
		testCSVAlphaVersion    = "1.2.0"
		testCSVReplacedVersion = "0.0.9"

		testOwnedCRDName = "mockserviceresource-v1.catalog.testing.coreos.com"
	)

	// Stable: v1.0.0 replaces v0.0.9
	testCSVResourceStable := createCSV(testStableCSVName, testCSVStableVersion,
		testReplacedCSVName, []string{testOwnedCRDName})

	// Beta: v1.1.0 replaces v0.0.9
	testCSVResourceBeta := createCSV(testBetaCSVName, testCSVBetaVersion,
		testReplacedCSVName, []string{testOwnedCRDName})

	// Alpha: v1.2.0 replaces v1.1.0 replaces v0.0.9
	testCSVResourceAlpha := createCSV(testAlphaCSVName, testCSVAlphaVersion,
		testBetaCSVName, []string{testOwnedCRDName})

	testCSVResourceReplaced := createCSV(testReplacedCSVName, testCSVReplacedVersion,
		"", []string{testOwnedCRDName})

	testOwnedCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testOwnedCRDName,
		},
	}

	catalog := NewInMem()
	catalog.setOrReplaceCRDDefinition(testOwnedCRDDefinition)

	catalog.AddOrReplaceService(testCSVResourceAlpha)
	catalog.AddOrReplaceService(testCSVResourceBeta)
	catalog.AddOrReplaceService(testCSVResourceStable)
	catalog.AddOrReplaceService(testCSVResourceReplaced)

	catalog.addPackageManifest(uiv1alpha1.PackageManifest{
		PackageName:        "mockservice",
		DefaultChannelName: "stable",
		Channels: []uiv1alpha1.PackageChannel{
			{
				Name:           "stable",
				CurrentCSVName: testStableCSVName,
			},
			{
				Name:           "beta",
				CurrentCSVName: testBetaCSVName,
			},
			{
				Name:           "alpha",
				CurrentCSVName: testAlphaCSVName,
			},
		},
	})

	// v0.0.9 -> v1.0.0
	stableCSV, err := catalog.FindReplacementCSVForPackageNameUnderChannel("mockservice", "stable", testReplacedCSVName)
	assert.NoError(t, err)
	assert.Equal(t, testStableCSVName, stableCSV.GetName())

	// v0.0.9 -> v1.1.0
	betaCSV, err := catalog.FindReplacementCSVForPackageNameUnderChannel("mockservice", "beta", testReplacedCSVName)
	assert.NoError(t, err)
	assert.Equal(t, testBetaCSVName, betaCSV.GetName())

	// v0.0.9 -> v1.1.0 -> v1.2.0
	betaCSVStep, err := catalog.FindReplacementCSVForPackageNameUnderChannel("mockservice", "alpha", testReplacedCSVName)
	assert.NoError(t, err)
	assert.Equal(t, testBetaCSVName, betaCSVStep.GetName())

	alphaCSV, err := catalog.FindReplacementCSVForPackageNameUnderChannel("mockservice", "alpha", testBetaCSVName)
	assert.NoError(t, err)
	assert.Equal(t, testAlphaCSVName, alphaCSV.GetName())

	_, err = catalog.FindReplacementCSVForPackageNameUnderChannel("mockservice", "unknown", testReplacedCSVName)
	assert.Error(t, err)

	// Check the CSVs for the CRD.
	found, err := catalog.ListLatestCSVsForCRD(CRDKey{Name: testOwnedCRDName})
	assert.NoError(t, err)
	assert.Equal(t, 3, len(found))
}

func TestListLatestCSVsForCRD(t *testing.T) {
	var (
		testStableCSVName   = "mockservice-operator.v1.0.0"
		testBetaCSVName     = "mockservice-operator.v1.1.0"
		testAlphaCSVName    = "mockservice-operator.v1.2.0"
		testReplacedCSVName = "mockservice-operator.v0.0.9"

		testCSVStableVersion   = "1.0.0"
		testCSVBetaVersion     = "1.1.0"
		testCSVAlphaVersion    = "1.2.0"
		testCSVReplacedVersion = "0.0.9"

		testSomeCRD    = "somecrd.catalog.testing.coreos.com"
		testAnotherCRD = "anothercrd.catalog.testing.coreos.com"
		testThirdCRD   = "thirdcrd.catalog.testing.coreos.com"
		testMiddleCRD  = "middlecrd.catalog.testing.coreos.com"
	)

	// v0.0.9 owns `somecrd`
	// v1.0.0 owns `somecrd` and `anothercrd`
	// v1.1.0 owns `somecrd` and `middlecrd`
	// v1.2.0 owns `somecrd` and `thirdcrd` but *not* `middlecrd`

	// Stable: v1.0.0 replaces v0.0.9
	testCSVResourceStable := createCSV(testStableCSVName, testCSVStableVersion,
		testReplacedCSVName, []string{testSomeCRD, testAnotherCRD})

	// Beta: v1.1.0 replaces v0.0.9
	testCSVResourceBeta := createCSV(testBetaCSVName, testCSVBetaVersion,
		testReplacedCSVName, []string{testSomeCRD, testMiddleCRD})

	// Alpha: v1.2.0 replaces v1.1.0 replaces v0.0.9
	testCSVResourceAlpha := createCSV(testAlphaCSVName, testCSVAlphaVersion,
		testBetaCSVName, []string{testSomeCRD, testThirdCRD})

	testCSVResourceReplaced := createCSV(testReplacedCSVName, testCSVReplacedVersion,
		"", []string{testSomeCRD})

	testSomeCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testSomeCRD,
		},
	}

	testAnotherCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testAnotherCRD,
		},
	}

	testThirdCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testThirdCRD,
		},
	}

	testMiddleCRDDefinition := v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: testMiddleCRD,
		},
	}

	catalog := NewInMem()
	catalog.setOrReplaceCRDDefinition(testSomeCRDDefinition)
	catalog.setOrReplaceCRDDefinition(testAnotherCRDDefinition)
	catalog.setOrReplaceCRDDefinition(testThirdCRDDefinition)
	catalog.setOrReplaceCRDDefinition(testMiddleCRDDefinition)

	catalog.AddOrReplaceService(testCSVResourceAlpha)
	catalog.AddOrReplaceService(testCSVResourceBeta)
	catalog.AddOrReplaceService(testCSVResourceStable)
	catalog.AddOrReplaceService(testCSVResourceReplaced)

	catalog.addPackageManifest(uiv1alpha1.PackageManifest{
		PackageName:        "mockservice",
		DefaultChannelName: "stable",
		Channels: []uiv1alpha1.PackageChannel{
			{
				Name:           "stable",
				CurrentCSVName: testStableCSVName,
			},
			{
				Name:           "beta",
				CurrentCSVName: testBetaCSVName,
			},
			{
				Name:           "alpha",
				CurrentCSVName: testAlphaCSVName,
			},
		},
	})

	assertChannels := func(csvs []CSVAndChannelInfo, expectedChannels ...string) {
		assert.Equal(t, len(expectedChannels), len(csvs), "Expected channels %v, found %v", expectedChannels, csvs)
		channelsFound := map[string]bool{}
		for _, csv := range csvs {
			channelsFound[csv.Channel.Name] = true
		}

		for _, expectedChannel := range expectedChannels {
			_, found := channelsFound[expectedChannel]
			assert.True(t, found, "Expected channel %s", expectedChannel)
		}
	}

	// Find the latest owners of `somecrd`. Should be alpha, beta and stable, with their latest
	// versions.
	someCSVs, err := catalog.ListLatestCSVsForCRD(CRDKey{Name: testSomeCRD})
	assert.NoError(t, err)
	assertChannels(someCSVs, "alpha", "beta", "stable")

	// Find the latest owners of `anothercrd`. Should only be beta.
	anotherCSVs, err := catalog.ListLatestCSVsForCRD(CRDKey{Name: testAnotherCRD})
	assert.NoError(t, err)
	assertChannels(anotherCSVs, "stable")

	// Find the latest owners of `thirdcrd`. Should only be alpha.
	thirdCSVs, err := catalog.ListLatestCSVsForCRD(CRDKey{Name: testThirdCRD})
	assert.NoError(t, err)
	assertChannels(thirdCSVs, "alpha")

	// Find the latest owners of `middlecrd`. Should be alpha and beta, with beta's CSV (1.1.0) for
	// both, since alpha removes it in its later version.
	middleCSVs, err := catalog.ListLatestCSVsForCRD(CRDKey{Name: testMiddleCRD})
	assert.NoError(t, err)
	assertChannels(middleCSVs, "alpha", "beta")

	assert.Equal(t, testBetaCSVName, middleCSVs[0].CSV.GetName())
	assert.Equal(t, testBetaCSVName, middleCSVs[1].CSV.GetName())
}
