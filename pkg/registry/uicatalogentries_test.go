package registry

import (
	"errors"
	"testing"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
	"github.com/coreos-inc/alm/pkg/client/clientfakes"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCustomCatalogStore(t *testing.T) {
	fakeClient := new(clientfakes.FakeUICatalogEntryInterface)

	store := CustomResourceCatalogStore{Client: fakeClient}

	testPackageName := "MockServiceName"
	testCSVName := "MockServiceName-v1"
	testCSVVersion := "0.2.4+alpha"

	manifest := v1alpha1.PackageManifest{
		PackageName: testPackageName,
		Channels: []v1alpha1.PackageChannel{
			{
				Name:           "alpha",
				CurrentCSVName: testCSVName,
			},
		},
	}
	csv := csvv1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       csvv1alpha1.ClusterServiceVersionCRDName,
			APIVersion: csvv1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        testCSVName,
			Namespace:   "alm-coreos-tests",
			Annotations: map[string]string{"tectonic-visiblity": "tectonic-feature"},
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			Version: *semver.New(testCSVVersion),
			CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
				Owned:    []csvv1alpha1.CRDDescription{},
				Required: []csvv1alpha1.CRDDescription{},
			},
		},
	}
	expectedEntry := v1alpha1.UICatalogEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.UICatalogEntryKind,
			APIVersion: v1alpha1.UICatalogEntryCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testCSVName,
			Namespace: "alm-coreos-tests",
			Labels:    map[string]string{"tectonic-visibility": "tectonic-feature"},
		},
		Spec: &v1alpha1.UICatalogEntrySpec{
			Manifest: v1alpha1.PackageManifest{
				PackageName: testPackageName,
				Channels: []v1alpha1.PackageChannel{
					{
						Name:           "alpha",
						CurrentCSVName: testCSVName,
					},
				},
			},
			CSVSpec: csvv1alpha1.ClusterServiceVersionSpec{
				Version: *semver.New(testCSVVersion),
				CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
					Owned:    []csvv1alpha1.CRDDescription{},
					Required: []csvv1alpha1.CRDDescription{},
				},
			},
		},
	}
	returnEntry := v1alpha1.UICatalogEntry{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	returnErr := errors.New("test error")

	fakeClient.UpdateEntryReturns(&returnEntry, returnErr)
	defer func() {
		require.Equal(t, 1, fakeClient.UpdateEntryCallCount())
		require.EqualValues(t, expectedEntry.Spec, fakeClient.UpdateEntryArgsForCall(0).Spec)
	}()

	actualEntry, err := store.Store(manifest, &csv)
	assert.Equal(t, returnErr, err)
	compareResources(t, &returnEntry, actualEntry)
}

func TestCustomCatalogStoreDefaultVisibility(t *testing.T) {
	fakeClient := new(clientfakes.FakeUICatalogEntryInterface)

	store := CustomResourceCatalogStore{Client: fakeClient}

	testPackageName := "MockServiceName"
	testCSVName := "MockServiceName-v1"
	testCSVVersion := "0.2.4+alpha"

	manifest := v1alpha1.PackageManifest{
		PackageName: testPackageName,
		Channels: []v1alpha1.PackageChannel{
			{
				Name:           "alpha",
				CurrentCSVName: testCSVName,
			},
		},
	}

	csv := csvv1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       csvv1alpha1.ClusterServiceVersionCRDName,
			APIVersion: csvv1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        testCSVName,
			Namespace:   "alm-coreos-tests",
			Annotations: map[string]string{}, // no visibility annotation
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			Version: *semver.New(testCSVVersion),
			CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
				Owned:    []csvv1alpha1.CRDDescription{},
				Required: []csvv1alpha1.CRDDescription{},
			},
		},
	}
	expectedEntry := v1alpha1.UICatalogEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.UICatalogEntryKind,
			APIVersion: v1alpha1.UICatalogEntryCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testCSVName,
			Namespace: "alm-coreos-tests",
			Labels:    map[string]string{"tectonic-visibility": "ocs"},
		},
		Spec: &v1alpha1.UICatalogEntrySpec{
			Manifest: v1alpha1.PackageManifest{
				PackageName: testPackageName,
				Channels: []v1alpha1.PackageChannel{
					{
						Name:           "alpha",
						CurrentCSVName: testCSVName,
					},
				},
			},
			CSVSpec: csvv1alpha1.ClusterServiceVersionSpec{
				Version: *semver.New(testCSVVersion),
				CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
					Owned:    []csvv1alpha1.CRDDescription{},
					Required: []csvv1alpha1.CRDDescription{},
				},
			},
		},
	}
	returnEntry := v1alpha1.UICatalogEntry{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	returnErr := errors.New("test error")

	fakeClient.UpdateEntryReturns(&returnEntry, returnErr)
	defer func() {
		require.Equal(t, 1, fakeClient.UpdateEntryCallCount())
		require.Equal(t, expectedEntry.Spec, fakeClient.UpdateEntryArgsForCall(0).Spec)
	}()

	actualEntry, err := store.Store(manifest, &csv)
	assert.Equal(t, returnErr, err)
	compareResources(t, &returnEntry, actualEntry)
}

func TestCustomResourceCatalogStoreSync(t *testing.T) {
	store := CustomResourceCatalogStore{Namespace: "alm-coreos-tests"}
	src := NewInMem()

	testCSVNameA := "MockServiceNameA-v1"
	testCSVVersionA1 := "0.2.4+alpha"
	testPackageA := v1alpha1.PackageManifest{
		PackageName: "MockServiceA",
		Channels: []v1alpha1.PackageChannel{
			{
				Name:           "alpha",
				CurrentCSVName: testCSVNameA,
			},
		},
	}

	testCSVNameB := "MockServiceNameB-v1"
	testCSVVersionB1 := "1.0.1"
	testPackageB := v1alpha1.PackageManifest{
		PackageName: "MockServiceB",
		Channels: []v1alpha1.PackageChannel{
			{
				Name:           "alpha",
				CurrentCSVName: testCSVNameB,
			},
		},
	}

	testCSVA1 := createCSV(testCSVNameA, testCSVVersionA1, "", []string{})
	testCSVB1 := createCSV(testCSVNameB, testCSVVersionB1, "", []string{})
	src.AddOrReplaceService(testCSVA1)
	src.AddOrReplaceService(testCSVB1)
	require.NoError(t, src.addPackageManifest(testPackageA))
	require.NoError(t, src.addPackageManifest(testPackageB))

	storeResults := []struct {
		ResultA1 *v1alpha1.UICatalogEntry
		ErrorA1  error

		ResultB1 *v1alpha1.UICatalogEntry
		ErrorB1  error

		ExpectedStatus         string
		ExpectedServicesSynced int
	}{
		{
			&v1alpha1.UICatalogEntry{ObjectMeta: metav1.ObjectMeta{Name: testCSVNameA}}, nil,
			&v1alpha1.UICatalogEntry{ObjectMeta: metav1.ObjectMeta{Name: testCSVNameB}}, nil,
			"success", 2,
		},
		{
			&v1alpha1.UICatalogEntry{ObjectMeta: metav1.ObjectMeta{Name: testCSVNameA}}, nil,
			nil, errors.New("test error"),
			"error", 1,
		},
		{
			nil, errors.New("test error1"),
			&v1alpha1.UICatalogEntry{ObjectMeta: metav1.ObjectMeta{Name: testCSVNameB}}, nil,
			"error", 1,
		},
	}

	for _, res := range storeResults {
		fakeClient := new(clientfakes.FakeUICatalogEntryInterface)
		store.Client = fakeClient

		fakeClient.UpdateEntryReturnsOnCall(0, res.ResultA1, res.ErrorA1)
		fakeClient.UpdateEntryReturnsOnCall(1, res.ResultB1, res.ErrorB1)

		entries, err := store.Sync(src)
		require.Equal(t, res.ExpectedServicesSynced, len(entries))
		require.Equal(t, res.ExpectedStatus, store.LastAttemptedSync.Status)
		require.NoError(t, err)
		require.Equal(t, 2, fakeClient.UpdateEntryCallCount())
	}
}
