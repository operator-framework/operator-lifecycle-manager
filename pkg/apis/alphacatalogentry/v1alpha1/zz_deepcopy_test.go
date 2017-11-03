package v1alpha1

import (
	"testing"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
)

func TestGeneratedDeepcopy(t *testing.T) {
	version, err := semver.NewVersion("0.0.0-pre")
	require.NoError(t, err)
	testSpec := &AlphaCatalogEntrySpec{
		v1alpha1.ClusterServiceVersionSpec{
			DisplayName: "TestAlphaCatalogEntry",
			Description: "This is a test app type",
			Keywords:    []string{"mock", "dev", "alm"},
			Maintainers: []v1alpha1.Maintainer{{
				Name:  "testbot",
				Email: "testbot@coreos.com",
			}},
			Links: []v1alpha1.AppLink{{
				Name: "ALM Homepage",
				URL:  "https://github.com/coreos-inc/alm",
			}},
			Icon: []v1alpha1.Icon{{
				Data:      "dGhpcyBpcyBhIHRlc3Q=",
				MediaType: "image/gif",
			}},
			Version: *version,
		},
	}

	testEntry := &AlphaCatalogEntry{Spec: testSpec}
	copyEntry := testEntry.DeepCopy()
	require.EqualValues(t, testEntry, copyEntry)
	testEntry = &AlphaCatalogEntry{Spec: nil}
	copyEntry = testEntry.DeepCopy()
	require.EqualValues(t, testEntry, copyEntry)
	copyEntryObj := testEntry.DeepCopyObject()
	require.EqualValues(t, testEntry, copyEntryObj)
	testEntry = nil
	require.Nil(t, testEntry.DeepCopy())
	require.Nil(t, testEntry.DeepCopyObject())

	testList := &AlphaCatalogEntryList{Items: []*AlphaCatalogEntrySpec{testSpec}}
	copyList := testList.DeepCopy()
	require.EqualValues(t, copyList, testList)
	testList = &AlphaCatalogEntryList{Items: []*AlphaCatalogEntrySpec{nil}}
	copyList = testList.DeepCopy()
	require.EqualValues(t, copyList, testList)
	copyListObj := testList.DeepCopyObject()
	require.EqualValues(t, testList, copyListObj)
	testList = nil
	require.Nil(t, testList.DeepCopy())
	require.Nil(t, testList.DeepCopyObject())

	require.EqualValues(t, testSpec, testSpec.DeepCopy())
	testSpec = nil
	require.Nil(t, testSpec.DeepCopy())
}
