package servicebroker

import (
	"testing"

	"github.com/stretchr/testify/require"

	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mockCSV() csvv1alpha1.ClusterServiceVersion {
	return csvv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-service.v1.0.0",
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			Description: "A cool description of this service",
		},
	}
}

func TestServiceClassLongDescription(t *testing.T) {
	type tester struct {
		InputCSV            csvv1alpha1.ClusterServiceVersion
		ExpectedDescription string
	}
	tests := []tester{
		{
			InputCSV: csvv1alpha1.ClusterServiceVersion{
				Spec: csvv1alpha1.ClusterServiceVersionSpec{
					Description: "A cool description of this service",
				},
			},
			ExpectedDescription: "A cool description of this service",
		},
		{
			InputCSV: csvv1alpha1.ClusterServiceVersion{
				Spec: csvv1alpha1.ClusterServiceVersionSpec{
					Description: "# A cool description of this service",
				},
			},
			ExpectedDescription: "A cool description of this service",
		},
	}
	for _, tt := range tests {
		desc := serviceClassLongDescription(tt.InputCSV)
		require.Equal(t, tt.ExpectedDescription, desc)
	}
}
