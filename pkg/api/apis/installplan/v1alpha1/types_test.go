package v1alpha1

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"

	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
)

func stepCRD(name, kind, group, version string) v1beta1.CustomResourceDefinition {
	return v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		TypeMeta: metav1.TypeMeta{
			Kind: kind,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   group,
			Version: version,
		},
	}
}

func stepRes(name, kind, group, version string) StepResource {
	return StepResource{
		Name:    name,
		Kind:    kind,
		Group:   group,
		Version: version,
	}
}

func stepCSV(name, kind, group, version string) csvv1alpha1.ClusterServiceVersion {
	return csvv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       kind,
			APIVersion: schema.GroupVersion{Group: group, Version: version}.String(),
		},
	}
}

func TestNewStepResourceFromCRD(t *testing.T) {
	var table = []struct {
		crd             v1beta1.CustomResourceDefinition
		expectedStepRes StepResource
		expectedError   error
	}{
		{stepCRD("", "", "", ""), stepRes("", "", "", ""), nil},
		{stepCRD("name", "", "", ""), stepRes("name", "", "", ""), nil},
		{stepCRD("name", "kind", "", ""), stepRes("name", "kind", "", ""), nil},
		{stepCRD("name", "kind", "group", ""), stepRes("name", "kind", "group", ""), nil},
		{stepCRD("name", "kind", "group", "version"), stepRes("name", "kind", "group", "version"), nil},
	}

	for _, tt := range table {
		crdSerializer := json.NewSerializer(json.DefaultMetaFactory, scheme.Scheme, scheme.Scheme, true)

		var expectedManifest bytes.Buffer
		if err := crdSerializer.Encode(&tt.crd, &expectedManifest); err != nil {
			require.Nil(t, err)
		}

		stepRes, err := NewStepResourceFromCRD(&tt.crd)
		require.Equal(t, tt.expectedError, err)
		require.Equal(t, tt.expectedStepRes.Name, stepRes.Name)
		require.Equal(t, tt.expectedStepRes.Kind, stepRes.Kind)
		require.Equal(t, tt.expectedStepRes.Group, stepRes.Group)
		require.Equal(t, tt.expectedStepRes.Version, stepRes.Version)
		require.JSONEq(t, expectedManifest.String(), stepRes.Manifest)
	}
}

func TestNewStepResourceFromCSV(t *testing.T) {
	var table = []struct {
		csv             csvv1alpha1.ClusterServiceVersion
		expectedStepRes StepResource
		expectedError   error
	}{
		{stepCSV("", "", "", ""), stepRes("", "", "", ""), nil},
		{stepCSV("name", "", "", ""), stepRes("name", "", "", ""), nil},
		{stepCSV("name", "kind", "", ""), stepRes("name", "kind", "", ""), nil},
		{stepCSV("name", "kind", "group", ""), stepRes("name", "kind", "group", ""), nil},
		{stepCSV("name", "kind", "group", "version"), stepRes("name", "kind", "group", "version"), nil},
	}

	for _, tt := range table {
		csvScheme := runtime.NewScheme()
		if err := csvv1alpha1.AddToScheme(csvScheme); err != nil {
			require.Nil(t, err)
		}
		csvSerializer := json.NewSerializer(json.DefaultMetaFactory, csvScheme, csvScheme, true)

		var expectedManifest bytes.Buffer
		if err := csvSerializer.Encode(&tt.csv, &expectedManifest); err != nil {
			require.Nil(t, err)
		}

		stepRes, err := NewStepResourceFromCSV(&tt.csv)
		require.Equal(t, tt.expectedError, err)
		require.Equal(t, tt.expectedStepRes.Name, stepRes.Name)
		require.Equal(t, tt.expectedStepRes.Kind, stepRes.Kind)
		require.Equal(t, tt.expectedStepRes.Group, stepRes.Group)
		require.Equal(t, tt.expectedStepRes.Version, stepRes.Version)
		require.JSONEq(t, expectedManifest.String(), stepRes.Manifest)
	}
}

type mockTime struct {
	testTime metav1.Time
}

func (m *mockTime) Copy() metav1.Time {
	wrapped := m.testTime.DeepCopy()
	return *wrapped
}

func TestUpdateConditionIn(t *testing.T) {
	var (
		installPlanTestConditionType1 InstallPlanConditionType = "test1"
		installPlanTestConditionType2 InstallPlanConditionType = "test2"
		installPlanTestConditionType3 InstallPlanConditionType = "test3"

		before  = metav1.Unix(1257800000, 0)
		recent  = metav1.Unix(1257894000, 0)
		nowtime = &mockTime{testTime: recent}
	)
	now = nowtime.Copy // set `now` function to mock
	table := []struct {
		Title      string
		Initial    []InstallPlanCondition
		Update     InstallPlanCondition
		Expected   InstallPlanCondition
		Conditions []InstallPlanCondition
	}{
		{
			Title: "Appends condition if list is empty",

			Initial: []InstallPlanCondition{},
			Update: InstallPlanCondition{
				Type:   installPlanTestConditionType1,
				Status: corev1.ConditionTrue,
			},
			Expected: InstallPlanCondition{
				Type:               installPlanTestConditionType1,
				Status:             corev1.ConditionTrue,
				LastUpdateTime:     recent,
				LastTransitionTime: recent,
			},
			Conditions: []InstallPlanCondition{
				InstallPlanCondition{
					Type:               installPlanTestConditionType1,
					Status:             corev1.ConditionTrue,
					LastUpdateTime:     recent,
					LastTransitionTime: recent,
				},
			},
		},
		{
			Title: "Appends condition if condition type not in list",

			Initial: []InstallPlanCondition{
				InstallPlanCondition{
					Type:   installPlanTestConditionType1,
					Status: corev1.ConditionTrue,
				},
			},
			Update: InstallPlanCondition{
				Type:   installPlanTestConditionType2,
				Status: corev1.ConditionTrue,
			},
			Expected: InstallPlanCondition{
				Type:               installPlanTestConditionType2,
				Status:             corev1.ConditionTrue,
				LastUpdateTime:     recent,
				LastTransitionTime: recent,
			},
			Conditions: []InstallPlanCondition{
				InstallPlanCondition{
					Type:   installPlanTestConditionType1,
					Status: corev1.ConditionTrue,
				},
				InstallPlanCondition{
					Type:               installPlanTestConditionType2,
					Status:             corev1.ConditionTrue,
					LastUpdateTime:     recent,
					LastTransitionTime: recent,
				},
			},
		},
		{
			Title: "updates condition in list to new state",

			Initial: []InstallPlanCondition{
				InstallPlanCondition{
					Type:   installPlanTestConditionType1,
					Status: corev1.ConditionFalse,
				},
				InstallPlanCondition{
					Type:   installPlanTestConditionType2,
					Status: corev1.ConditionUnknown,
				},
				InstallPlanCondition{
					Type:   installPlanTestConditionType3,
					Status: corev1.ConditionTrue,
				},
			},
			Update: InstallPlanCondition{
				Type:   installPlanTestConditionType2,
				Status: corev1.ConditionTrue,
			},
			Expected: InstallPlanCondition{
				Type:               installPlanTestConditionType2,
				Status:             corev1.ConditionTrue,
				LastUpdateTime:     recent,
				LastTransitionTime: recent,
			},

			Conditions: []InstallPlanCondition{
				InstallPlanCondition{
					Type:   installPlanTestConditionType1,
					Status: corev1.ConditionFalse,
				},
				InstallPlanCondition{
					Type:               installPlanTestConditionType2,
					Status:             corev1.ConditionTrue,
					LastUpdateTime:     recent,
					LastTransitionTime: recent,
				},
				InstallPlanCondition{
					Type:   installPlanTestConditionType3,
					Status: corev1.ConditionTrue,
				},
			},
		},
		{
			Title: "updates lastupdatetime in list when status of condition didn't change",

			Initial: []InstallPlanCondition{
				InstallPlanCondition{
					Type:   installPlanTestConditionType1,
					Status: corev1.ConditionFalse,
				},
				InstallPlanCondition{
					Type:               installPlanTestConditionType2,
					Status:             corev1.ConditionTrue,
					LastUpdateTime:     before,
					LastTransitionTime: before,
				},
				InstallPlanCondition{
					Type:   installPlanTestConditionType3,
					Status: corev1.ConditionTrue,
				},
			},
			Update: InstallPlanCondition{
				Type:   installPlanTestConditionType2,
				Status: corev1.ConditionTrue,
			},
			Expected: InstallPlanCondition{
				Type:               installPlanTestConditionType2,
				Status:             corev1.ConditionTrue,
				LastUpdateTime:     recent,
				LastTransitionTime: before,
			},
			Conditions: []InstallPlanCondition{
				InstallPlanCondition{
					Type:   installPlanTestConditionType1,
					Status: corev1.ConditionFalse,
				},
				InstallPlanCondition{
					Type:               installPlanTestConditionType2,
					Status:             corev1.ConditionTrue,
					LastUpdateTime:     recent,
					LastTransitionTime: before,
				},
				InstallPlanCondition{
					Type:   installPlanTestConditionType3,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	for _, tt := range table {
		status := &InstallPlanStatus{Conditions: tt.Initial}
		actual := status.SetCondition(tt.Update)
		require.EqualValues(t, tt.Expected, actual, tt.Title)
		require.EqualValues(t, tt.Conditions, status.Conditions, tt.Title)
	}
}
