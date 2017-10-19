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

func TestCSVOwnsCRD(t *testing.T) {
	var table = []struct {
		ownedCRDNames []string
		crdName       string
		expected      bool
	}{
		{nil, "", false},
		{nil, "querty", false},
		{[]string{}, "", false},
		{[]string{}, "querty", false},
		{[]string{"owned"}, "owned", true},
		{[]string{"owned"}, "notOwned", false},
		{[]string{"first", "second"}, "first", true},
		{[]string{"first", "second"}, "second", true},
		{[]string{"first", "second"}, "third", false},
	}

	for _, tt := range table {
		// Build a list of CRDDescription used in the CSV.
		var ownedDescriptions []v1alpha1csv.CRDDescription
		for _, crdName := range tt.ownedCRDNames {
			ownedDescriptions = append(ownedDescriptions, v1alpha1csv.CRDDescription{
				Name: crdName,
			})
		}

		// Create a blank CSV with the owned descriptions.
		csv := v1alpha1csv.ClusterServiceVersion{
			Spec: v1alpha1csv.ClusterServiceVersionSpec{
				CustomResourceDefinitions: v1alpha1csv.CustomResourceDefinitions{
					Owned: ownedDescriptions,
				},
			},
		}

		// Call csvOwnsCRD and ensure the result is as expected.
		require.Equal(t, tt.expected, csvOwnsCRD(csv, tt.crdName))
	}
}

type mockTransitioner struct {
	err error
}

var _ installPlanTransitioner = &mockTransitioner{}

func (m *mockTransitioner) CreatePlan(plan *v1alpha1.InstallPlan) error {
	return m.err
}

func (m *mockTransitioner) ExecutePlan(plan *v1alpha1.InstallPlan) error {
	return m.err
}

func TestTransitionInstallPlan(t *testing.T) {
	var table = []struct {
		initial    v1alpha1.InstallPlanPhase
		transError error
		expected   v1alpha1.InstallPlanPhase
	}{
		{v1alpha1.InstallPlanPhaseNone, nil, v1alpha1.InstallPlanPhasePlanning},
		{v1alpha1.InstallPlanPhaseNone, errors.New(""), v1alpha1.InstallPlanPhasePlanning},
		{v1alpha1.InstallPlanPhasePlanning, nil, v1alpha1.InstallPlanPhaseInstalling},
		{v1alpha1.InstallPlanPhasePlanning, errors.New(""), v1alpha1.InstallPlanPhasePlanning},
		{v1alpha1.InstallPlanPhaseInstalling, nil, v1alpha1.InstallPlanPhaseComplete},
		{v1alpha1.InstallPlanPhaseInstalling, errors.New(""), v1alpha1.InstallPlanPhaseInstalling},
	}

	for _, tt := range table {
		plan := &v1alpha1.InstallPlan{
			Status: v1alpha1.InstallPlanStatus{
				InstallPlanCondition: v1alpha1.InstallPlanCondition{
					Phase: tt.initial,
				},
			},
		}

		transitioner := &mockTransitioner{tt.transError}
		transitionInstallPlanState(transitioner, plan)
		require.Equal(t, tt.expected, plan.Status.Phase)
	}
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
