package e2e

import (
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	pmv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
)

func TestPackageManifestLoading(t *testing.T) {
	// create a simple catalogsource
	packageName := genName("nginx")
	stableChannel := "stable"
	packageStable := packageName + "-stable"
	manifests := []registry.PackageManifest{
		registry.PackageManifest{
			PackageName: packageName,
			Channels: []registry.PackageChannel{
				registry.PackageChannel{Name: stableChannel, CurrentCSVName: packageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"
	crd := newCRD(crdName, testNamespace, crdPlural)
	namedStrategy := newNginxInstallStrategy(genName("dep-"))
	csv := newCSV(packageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{crd}, nil, namedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)

	catalogSourceName := genName("mock-ocs")
	_, cleanupCatalogSource, err := createInternalCatalogSource(t, c, crc, catalogSourceName, testNamespace, manifests, []extv1beta1.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
	require.NoError(t, err)
	defer cleanupCatalogSource()

	expectedStatus := pmv1alpha1.PackageManifestStatus{
		CatalogSourceName:      catalogSourceName,
		CatalogSourceNamespace: testNamespace,
		PackageName:            packageName,
		Channels: []pmv1alpha1.PackageChannel{
			pmv1alpha1.PackageChannel{Name: stableChannel, CurrentCSVName: packageStable},
		},
		DefaultChannelName: stableChannel,
	}

	// TODO: Ensure catalog source is tracked by package server before checking
	pmc := newPMClient(t)
	pm, err := pmc.Packagemanifest().PackageManifests(testNamespace).Get(packageName, metav1.GetOptions{})

	// check parsed PackageManifest
	require.NoError(t, err, "error getting package manifest")
	t.Logf("packagemanifest: %v", pm)
	require.NotNil(t, pm)
	require.Equal(t, packageName, pm.GetName())
	require.Equal(t, expectedStatus, pm.Status)
}
