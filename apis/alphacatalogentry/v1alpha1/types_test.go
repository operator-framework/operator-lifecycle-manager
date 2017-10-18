package v1alpha1

//
//func TestRegisterAlphaCatalogEntry(t *testing.T) {
//	version, err := semver.NewVersion("0.0.0-pre")
//	require.NoError(t, err)
//
//	testApp := &AlphaCatalogEntrySpec{
//		v1alpha1.ClusterServiceVersionSpec{
//			DisplayName: "TestAlphaCatalogEntry",
//			Description: "This is a test app type",
//			Keywords:    []string{"mock", "dev", "alm"},
//			Maintainers: []v1alpha1.Maintainer{{
//				Name:  "testbot",
//				Email: "testbot@coreos.com",
//			}},
//			Links: []v1alpha1.AppLink{{
//				Name: "ALM Homepage",
//				URL:  "https://github.com/coreos-inc/alm",
//			}},
//			Icon: []v1alpha1.Icon{{
//				Data:      "dGhpcyBpcyBhIHRlc3Q=",
//				MediaType: "image/gif",
//			}},
//			Version: *version,
//		},
//	}
//
//	//rsrc := NewAlphaCatalogEntryResource(testApp)
//
//
//}
