package catalog

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	csvv1alpha1 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/apis/installplan/v1alpha1"
	catlib "github.com/coreos-inc/alm/catalog"
)

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
	var table = []struct {
		plan            v1alpha1.InstallPlan
		source          catlib.Source
		expectedErr     error
		expectedPlanLen int
	}{
		{installPlan("error"), TestSource{findCSVErr: errors.New("")}, errors.New(""), 0},
		{installPlan("name"), TestSource{csv: csv("", "", []string{"error"}, nil), findCRDErr: errors.New("")}, errors.New(""), 0},
		{installPlan("name"), TestSource{csv: csv("", "", []string{"crdName"}, nil)}, nil, 2},
	}

	for _, tt := range table {
		err := createInstallPlan(tt.source, &tt.plan)
		require.Equal(t, tt.expectedErr, err)
		require.Equal(t, tt.expectedPlanLen, len(tt.plan.Status.Plan))
	}
}

func installPlan(names ...string) v1alpha1.InstallPlan {
	return v1alpha1.InstallPlan{
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: names,
		},
		Status: v1alpha1.InstallPlanStatus{
			Plan: []v1alpha1.Step{},
		},
	}
}

func csv(name, kind string, required, owned []string) csvv1alpha1.ClusterServiceVersion {
	requiredCRDDescs := make([]csvv1alpha1.CRDDescription, 0)
	for _, name := range required {
		requiredCRDDescs = append(requiredCRDDescs, csvv1alpha1.CRDDescription{Name: name})
	}

	ownedCRDDescs := make([]csvv1alpha1.CRDDescription, 0)
	for _, name := range owned {
		ownedCRDDescs = append(ownedCRDDescs, csvv1alpha1.CRDDescription{Name: name})
	}

	return csvv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		TypeMeta: metav1.TypeMeta{
			Kind: kind,
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
				Required: requiredCRDDescs,
				Owned:    ownedCRDDescs,
			},
		},
	}
}

type TestSource struct {
	findCSVErr       error
	findCRDErr       error
	findCSVForCRDErr error
	csv              csvv1alpha1.ClusterServiceVersion
	crd              v1beta1.CustomResourceDefinition
}

var _ catlib.Source = TestSource{}

func (ts TestSource) FindLatestCSVByServiceName(name string) (*csvv1alpha1.ClusterServiceVersion, error) {
	return &ts.csv, ts.findCSVErr
}

func (ts TestSource) FindCSVByServiceNameAndVersion(name, version string) (*csvv1alpha1.ClusterServiceVersion, error) {
	return nil, nil
}

func (ts TestSource) ListCSVsForServiceName(name string) ([]csvv1alpha1.ClusterServiceVersion, error) {
	return nil, nil
}
func (ts TestSource) ListServices() ([]csvv1alpha1.ClusterServiceVersion, error) {
	return nil, nil
}

func (ts TestSource) FindCRDByName(name string) (*v1beta1.CustomResourceDefinition, error) {
	return &ts.crd, ts.findCRDErr
}

func (ts TestSource) FindLatestCSVForCRD(crdname string) (*csvv1alpha1.ClusterServiceVersion, error) {
	return &ts.csv, ts.findCSVForCRDErr
}

func (ts TestSource) ListCSVsForCRD(crdname string) ([]csvv1alpha1.ClusterServiceVersion, error) {
	return nil, nil
}
