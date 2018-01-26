package v1alpha1

import (
	"testing"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
)

func TestRegisterUICatalogEntry(t *testing.T) {
	version, err := semver.NewVersion("0.0.0-pre")
	require.NoError(t, err)

	testApp := &UICatalogEntrySpec{
		Manifest: PackageManifest{
			PackageName: "TestPackage",
			Channels: []PackageChannel{
				{
					Name:           "alpha",
					CurrentCSVName: "testpackagename",
				},
			},
		},
		CSVSpec: v1alpha1.ClusterServiceVersionSpec{
			DisplayName: "TestUICatalogEntry",
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

	rsrc := NewUICatalogEntryResource(testApp)
	require.NotNil(t, rsrc)
}
