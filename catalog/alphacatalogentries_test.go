package catalog

import (
	"errors"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/apis/alphacatalogentry/v1alpha1"
	csvv1alpha1 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/client"
)

func TestCustomCatalogStore(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := client.NewMockAlphaCatalogEntryInterface(ctrl)
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
			Name:      testCSVName,
			Namespace: "alm-coreos-tests",
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			Version: *semver.New(testCSVVersion),
			CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
				Owned:    []csvv1alpha1.CRDDescription{},
				Required: []csvv1alpha1.CRDDescription{},
			},
		},
	}
	expectedEntry := v1alpha1.AlphaCatalogEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.AlphaCatalogEntryCRDName,
			APIVersion: v1alpha1.AlphaCatalogEntryCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testCSVName,
			Namespace: "alm-coreos-tests",
		},
		Spec: &v1alpha1.AlphaCatalogEntrySpec{
			ClusterServiceVersionSpec: csvv1alpha1.ClusterServiceVersionSpec{
				Version: *semver.New(testCSVVersion),
				CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
					Owned:    []csvv1alpha1.CRDDescription{},
					Required: []csvv1alpha1.CRDDescription{},
				},
			},
		},
	}
	returnEntry := v1alpha1.AlphaCatalogEntry{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	returnErr := errors.New("test error")
	mockClient.EXPECT().UpdateEntry(&expectedEntry).Return(&returnEntry, returnErr)

	actualEntry, err := store.Store(&csv)
	assert.Equal(t, returnErr, err)
	compareResources(t, &returnEntry, actualEntry)
}
