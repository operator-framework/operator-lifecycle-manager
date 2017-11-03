package v1alpha1

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
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
