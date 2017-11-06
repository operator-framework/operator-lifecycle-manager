package catalog

import (
	"errors"
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	catlib "github.com/coreos-inc/alm/pkg/catalog"
)

type mockTransitioner struct {
	err error
}

var _ installPlanTransitioner = &mockTransitioner{}

func (m *mockTransitioner) ResolvePlan(plan *v1alpha1.InstallPlan) error {
	return m.err
}

func (m *mockTransitioner) ExecutePlan(plan *v1alpha1.InstallPlan) error {
	return m.err
}

func TestTransitionInstallPlan(t *testing.T) {
	var (
		errMsg = "transition test error"
		err    = errors.New(errMsg)

		resolved = &v1alpha1.InstallPlanCondition{
			Type:   v1alpha1.InstallPlanResolved,
			Status: v1.ConditionTrue,
		}
		unresolved = &v1alpha1.InstallPlanCondition{
			Type:    v1alpha1.InstallPlanResolved,
			Status:  v1.ConditionFalse,
			Reason:  v1alpha1.InstallPlanReasonDependencyConflict,
			Message: errMsg,
		}
		installed = &v1alpha1.InstallPlanCondition{
			Type:   v1alpha1.InstallPlanInstalled,
			Status: v1.ConditionTrue,
		}
		failed = &v1alpha1.InstallPlanCondition{
			Type:    v1alpha1.InstallPlanInstalled,
			Status:  v1.ConditionFalse,
			Reason:  v1alpha1.InstallPlanReasonComponentFailed,
			Message: errMsg,
		}
	)
	var table = []struct {
		initial    v1alpha1.InstallPlanPhase
		transError error
		expected   v1alpha1.InstallPlanPhase
		condition  *v1alpha1.InstallPlanCondition
	}{
		{v1alpha1.InstallPlanPhaseNone, nil, v1alpha1.InstallPlanPhasePlanning, nil},
		{v1alpha1.InstallPlanPhaseNone, err, v1alpha1.InstallPlanPhasePlanning, nil},

		{v1alpha1.InstallPlanPhasePlanning, nil, v1alpha1.InstallPlanPhaseInstalling, resolved},
		{v1alpha1.InstallPlanPhasePlanning, err, v1alpha1.InstallPlanPhasePlanning, unresolved},

		{v1alpha1.InstallPlanPhaseInstalling, nil, v1alpha1.InstallPlanPhaseComplete, installed},
		{v1alpha1.InstallPlanPhaseInstalling, err, v1alpha1.InstallPlanPhaseInstalling, failed},
	}
	for _, tt := range table {
		// Create a plan in the provided initial phase.
		plan := &v1alpha1.InstallPlan{
			Status: v1alpha1.InstallPlanStatus{
				Phase:      tt.initial,
				Conditions: []v1alpha1.InstallPlanCondition{},
			},
		}

		// Create a transitioner that returns the provided error.
		transitioner := &mockTransitioner{tt.transError}

		// Attempt to transition phases.
		transitionInstallPlanState(transitioner, plan)

		// Assert that the final phase is as expected.
		require.Equal(t, tt.expected, plan.Status.Phase)

		// Assert that the condition set is as expected
		if tt.condition == nil {
			require.Equal(t, 0, len(plan.Status.Conditions))
		} else {
			require.Equal(t, 1, len(plan.Status.Conditions))
			require.Equal(t, tt.condition.Type, plan.Status.Conditions[0].Type)
			require.Equal(t, tt.condition.Status, plan.Status.Conditions[0].Status)
			require.Equal(t, tt.condition.Reason, plan.Status.Conditions[0].Reason)
			require.Equal(t, tt.condition.Message, plan.Status.Conditions[0].Message)
		}
	}
}

func TestResolveInstallPlan(t *testing.T) {
	type csvNames struct {
		name     string
		owned    []string
		required []string
	}
	var table = []struct {
		description     string
		planCSVName     string
		csv             []csvNames
		crdNames        []string
		expectedErr     error
		expectedPlanLen int
	}{
		{"MissingCSV", "name", []csvNames{{"", nil, nil}}, nil, errors.New("not found: ClusterServiceVersion name"), 0},
		{"MissingCSVByName", "name", []csvNames{{"missingName", nil, nil}}, nil, errors.New("not found: ClusterServiceVersion name"), 0},
		{"FoundCSV", "name", []csvNames{{"name", nil, nil}}, nil, nil, 1},
		{"CSVWithMissingOwnedCRD", "name", []csvNames{{"name", []string{"missingCRD"}, nil}}, nil, errors.New("not found: CRD missingCRD/missingCRD/v1"), 0},
		{"CSVWithMissingRequiredCRD", "name", []csvNames{{"name", nil, []string{"missingCRD"}}}, nil, errors.New("not found: CRD missingCRD/missingCRD/v1"), 0},
		{"FoundCSVWithCRD", "name", []csvNames{{"name", []string{"CRD"}, nil}}, []string{"CRD"}, nil, 2},
		{"FoundCSVWithDependency", "name", []csvNames{{"name", nil, []string{"CRD"}}, {"crdOwner", []string{"CRD"}, nil}}, []string{"CRD"}, nil, 3},
	}

	for _, tt := range table {
		t.Run(tt.description, func(t *testing.T) {
			log.SetLevel(log.DebugLevel)
			// Create a plan that is attempting to install the planCSVName.
			plan := installPlan(tt.planCSVName)

			// Create a catalog source containing a CSVs and CRDs with the provided
			// names.
			src := catlib.NewInMem()
			for _, name := range tt.crdNames {
				err := src.SetCRDDefinition(crd(name))
				require.NoError(t, err)
			}
			for _, names := range tt.csv {
				// We add unsafe so that we can test invalid states
				src.AddOrReplaceService(csv(names.name, names.owned, names.required))
			}

			// Resolve the plan.
			err := resolveInstallPlan(src, &plan)

			// Assert the error is as expected.
			if tt.expectedErr == nil {
				require.Nil(t, err)
			} else {
				require.Equal(t, tt.expectedErr, err)
			}

			// Assert the number of items in the plan are equal.
			require.Equal(t, tt.expectedPlanLen, len(plan.Status.Plan))
		})
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

func csv(name string, owned, required []string) csvv1alpha1.ClusterServiceVersion {
	requiredCRDDescs := make([]csvv1alpha1.CRDDescription, 0)
	for _, name := range required {
		requiredCRDDescs = append(requiredCRDDescs, csvv1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}

	ownedCRDDescs := make([]csvv1alpha1.CRDDescription, 0)
	for _, name := range owned {
		ownedCRDDescs = append(ownedCRDDescs, csvv1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}

	return csvv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
				Owned:    ownedCRDDescs,
				Required: requiredCRDDescs,
			},
		},
	}
}

func crd(name string) v1beta1.CustomResourceDefinition {
	return v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   name + "group",
			Version: "v1",
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}
