package catalog

import (
	"errors"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"reflect"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
)

type EntryMatcher struct{ entry v1alpha1.UICatalogEntry }

func MatchesEntry(entry v1alpha1.UICatalogEntry) gomock.Matcher {
	return &EntryMatcher{entry}
}

func (e *EntryMatcher) Matches(x interface{}) bool {
	entry, ok := x.(*v1alpha1.UICatalogEntry)
	if !ok {
		return false
	}
	return reflect.DeepEqual(entry.Spec, e.entry.Spec)
}

func (e *EntryMatcher) String() string {
	return "matches expected entry"
}

func MatchesService(service csvv1alpha1.ClusterServiceVersion) gomock.Matcher {
	return &EntryMatcher{v1alpha1.UICatalogEntry{Spec: &v1alpha1.UICatalogEntrySpec{ClusterServiceVersionSpec: service.Spec}}}
}

func TestCustomCatalogStore(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := NewMockUICatalogEntryInterface(ctrl)
	defer ctrl.Finish()

	store := CustomResourceCatalogStore{Client: mockClient}

	testCSVName := "MockServiceName-v1"
	testCSVVersion := "0.2.4+alpha"

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
			ClusterServiceVersionSpec: csvv1alpha1.ClusterServiceVersionSpec{
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
	mockClient.EXPECT().UpdateEntry(MatchesEntry(expectedEntry)).Return(&returnEntry, returnErr)

	actualEntry, err := store.Store(&csv)
	assert.Equal(t, returnErr, err)
	compareResources(t, &returnEntry, actualEntry)
}

func TestCustomCatalogStoreDefaultVisibility(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := NewMockUICatalogEntryInterface(ctrl)
	defer ctrl.Finish()

	store := CustomResourceCatalogStore{Client: mockClient}

	testCSVName := "MockServiceName-v1"
	testCSVVersion := "0.2.4+alpha"

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
			ClusterServiceVersionSpec: csvv1alpha1.ClusterServiceVersionSpec{
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
	mockClient.EXPECT().UpdateEntry(MatchesEntry(expectedEntry)).Return(&returnEntry, returnErr)

	actualEntry, err := store.Store(&csv)
	assert.Equal(t, returnErr, err)
	compareResources(t, &returnEntry, actualEntry)
}

func TestCustomResourceCatalogStoreSync(t *testing.T) {

	ctrl := gomock.NewController(t)
	mockClient := NewMockUICatalogEntryInterface(ctrl)
	defer ctrl.Finish()

	store := CustomResourceCatalogStore{Client: mockClient, Namespace: "alm-coreos-tests"}
	src := NewInMem()

	testCSVNameA := "MockServiceNameA-v1"
	testCSVVersionA1 := "0.2.4+alpha"

	testCSVNameB := "MockServiceNameB-v1"
	testCSVVersionB1 := "1.0.1"

	testCSVA1 := createCSV(testCSVNameA, testCSVVersionA1, "", []string{})
	testCSVB1 := createCSV(testCSVNameB, testCSVVersionB1, "", []string{})
	src.AddOrReplaceService(testCSVA1)
	src.AddOrReplaceService(testCSVB1)

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
		mockClient.EXPECT().UpdateEntry(MatchesService(testCSVA1)).Return(res.ResultA1, res.ErrorA1)
		mockClient.EXPECT().UpdateEntry(MatchesService(testCSVB1)).Return(res.ResultB1, res.ErrorB1)
		entries, err := store.Sync(src)
		assert.Equal(t, res.ExpectedServicesSynced, len(entries))
		assert.Equal(t, res.ExpectedStatus, store.LastAttemptedSync.Status)
		assert.NoError(t, err)
	}

}
