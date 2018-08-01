package servicebroker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mockCSV() v1alpha1.ClusterServiceVersion {
	return v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-service.v1.0.0",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Description: "A cool description of this service",
		},
	}
}

func TestServiceClassLongDescription(t *testing.T) {
	type tester struct {
		InputCSV            v1alpha1.ClusterServiceVersion
		ExpectedDescription string
	}
	tests := []tester{
		{
			InputCSV: v1alpha1.ClusterServiceVersion{
				Spec: v1alpha1.ClusterServiceVersionSpec{
					Description: "A cool description of this service",
				},
			},
			ExpectedDescription: "A cool description of this service",
		},
		{
			InputCSV: v1alpha1.ClusterServiceVersion{
				Spec: v1alpha1.ClusterServiceVersionSpec{
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
