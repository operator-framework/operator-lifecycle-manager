package catalog

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1csv "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/apis/installplan/v1alpha1"
)

func TestCheckIfOwned(t *testing.T) {
	crdName := "ownedCRD"
	csv := v1alpha1csv.ClusterServiceVersion{}
	owned := checkIfOwned(csv, crdName)
	require.False(t, owned)
	csv.Spec.CustomResourceDefinitions.Owned = []v1alpha1csv.CRDDescription{{Name: "notownedCRD"}}
	owned = checkIfOwned(csv, crdName)
	require.False(t, owned)
	csv.Spec.CustomResourceDefinitions.Owned = []v1alpha1csv.CRDDescription{{Name: "ownedCRD"}}
	owned = checkIfOwned(csv, crdName)
	require.True(t, owned)
}

func TestCreateInstallPlan(t *testing.T) {
	installPlan := &v1alpha1.InstallPlan{
		Status: v1alpha1.InstallPlanStatus{Plan: []v1alpha1.Step{}},
		Spec:   v1alpha1.InstallPlanSpec{},
	}
	installPlan.Spec.ClusterServiceVersionNames = []string{"error"}
	testSource := TestSource{}
	err := createInstallPlan(testSource, installPlan)
	require.Error(t, err)

	installPlan.Spec.ClusterServiceVersionNames = []string{"name"}
	testSource.csv = &v1alpha1csv.ClusterServiceVersion{
		Spec: v1alpha1csv.ClusterServiceVersionSpec{
			CustomResourceDefinitions: v1alpha1csv.CustomResourceDefinitions{
				Required: []v1alpha1csv.CRDDescription{{Name: "error"}}},
		},
	}
	err = createInstallPlan(testSource, installPlan)
	require.Error(t, err)

	installPlan.Spec.ClusterServiceVersionNames = []string{"name"}
	testSource.csv.Spec.CustomResourceDefinitions.Required = []v1alpha1csv.CRDDescription{{Name: "name"}}
	crd := &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "name",
		},
	}
	testSource.crd = crd
	testSource.csv.Name = "name"
	testSource.csv.Kind = "kind"
	testSource.csv.Spec.CustomResourceDefinitions.Owned = []v1alpha1csv.CRDDescription{{Name: "name"}}

	err = createInstallPlan(testSource, installPlan)
	require.NotEmpty(t, installPlan.Status.Plan)
	require.Equal(t, 2, len(installPlan.Status.Plan))
}

type TestSource struct {
	csv *v1alpha1csv.ClusterServiceVersion
	crd *v1beta1.CustomResourceDefinition
}

func (ts TestSource) FindLatestCSVByServiceName(name string) (*v1alpha1csv.ClusterServiceVersion, error) {
	if name == "error" {
		return nil, errors.New("FindLatestCSVByServiceName error")
	}
	return ts.csv, nil
}

func (ts TestSource) FindCSVByServiceNameAndVersion(name, version string) (*v1alpha1csv.ClusterServiceVersion, error) {
	return nil, nil
}

func (ts TestSource) ListCSVsForServiceName(name string) ([]v1alpha1csv.ClusterServiceVersion, error) {
	return nil, nil
}
func (ts TestSource) ListServices() ([]v1alpha1csv.ClusterServiceVersion, error) {
	return nil, nil
}

func (ts TestSource) FindCRDByName(name string) (*v1beta1.CustomResourceDefinition, error) {
	if name == "error" {
		return nil, errors.New("FindCRDByName error")
	}
	return ts.crd, nil
}

func (ts TestSource) FindLatestCSVForCRD(crdname string) (*v1alpha1csv.ClusterServiceVersion, error) {
	if crdname == "error" {
		return nil, errors.New("FindLatestCSVForCRD error")
	}
	return ts.csv, nil
}

func (ts TestSource) ListCSVsForCRD(crdname string) ([]v1alpha1csv.ClusterServiceVersion, error) {
	return nil, nil
}
