package catalog

import (
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/require"
	v1alpha12 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/apis/installplan/v1alpha1"
	csvV1alpha1 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/catalog"
	"errors"
)

func TestCheckIfOwned(t *testing.T) {
	ownerRefs := []v1.OwnerReference{{}}
	csv := v1alpha12.ClusterServiceVersion{}
	owned := checkIfOwned(csv, ownerRefs)
	require.False(t, owned)
	ownerRefs[0].Name = "name"
	csv.Name = "name"
	owned = checkIfOwned(csv, ownerRefs)
	require.False(t, owned)
	ownerRefs[0].Kind = "kind"
	csv.Kind = "kind"
	owned = checkIfOwned(csv, ownerRefs)
	require.True(t, owned)
}

func TestCreateInstallPlan(t *testing.T) {
	installPlan := &v1alpha1.InstallPlan{
		Status: v1alpha1.InstallPlanStatus{Plan: []v1alpha1.Step{}},
		Spec:   &v1alpha1.InstallPlanSpec{},
	}
	installPlan.Spec.ClusterServiceVersionNames = []string{"error"}
	testSource := TestSource{}
	err := createInstallPlan(testSource, installPlan)
	require.Error(t, err)

	installPlan.Spec.ClusterServiceVersionNames = []string{"name"}
	testSource.csv = &csvV1alpha1.ClusterServiceVersion{
		Spec: csvV1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: csvV1alpha1.CustomResourceDefinitions{
				Required: []csvV1alpha1.CRDDescription{{Name:"error"}}},
		},
	}
	err = createInstallPlan(testSource, installPlan)
	require.Error(t, err)

	installPlan.Spec.ClusterServiceVersionNames = []string{"name"}
	testSource.csv.Spec.CustomResourceDefinitions.Required = []csvV1alpha1.CRDDescription{{Name:"name"}}
	crd := &apiextensions.CustomResourceDefinition{
		ObjectMeta: v1.ObjectMeta{
			Name: "name",
			OwnerReferences: []v1.OwnerReference{
				{
					Kind: "kind", Name: "name",
				},
			},
		},
	}
	testSource.crd = &catalog.CRDWithManifest{CRD: crd}
	testSource.csv.Name = "name"
	testSource.csv.Kind = "kind"
	err = createInstallPlan(testSource, installPlan)
	require.NotEmpty(t, installPlan.Status.Plan)
	require.Equal(t, 1, len(installPlan.Status.Plan))
}

type TestSource struct {
	csv *csvV1alpha1.ClusterServiceVersion
	crd *catalog.CRDWithManifest
}

func (ts TestSource) FindLatestCSVByServiceName(name string) (*csvV1alpha1.ClusterServiceVersion, error) {
	if name == "error" {
		return nil, errors.New("FindLatestCSVByServiceName error")
	}
	return ts.csv, nil
}

func (ts TestSource) FindCSVByServiceNameAndVersion(name, version string) (*csvV1alpha1.ClusterServiceVersion, error) {
	return nil, nil
}

func (ts TestSource) ListCSVsForServiceName(name string) ([]csvV1alpha1.ClusterServiceVersion, error) {
	return nil, nil
}

func (ts TestSource) FindCRDByName(name string) (*catalog.CRDWithManifest, error) {
	if name == "error" {
		return nil, errors.New("FindCRDByName error")
	}
	return ts.crd, nil
}

func (ts TestSource) FindLatestCSVForCRD(crdname string) (*csvV1alpha1.ClusterServiceVersion, error) {
	if crdname == "error" {
		return nil, errors.New("FindLatestCSVForCRD error")
	}
	return ts.csv, nil
}

func (ts TestSource) ListCSVsForCRD(crdname string) ([]csvV1alpha1.ClusterServiceVersion, error) {
	return nil, nil
}
