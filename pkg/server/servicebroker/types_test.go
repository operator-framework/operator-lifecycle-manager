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

func TestCSVToServiceParsesDescription(t *testing.T) {
	var parseDesc = func(desc string) string {
		require.Equal(t, mockCSV().Spec.Description, desc)

		return ""
	}

	_, err := csvToService(mockCSV(), parseDesc)

	require.NoError(t, err)
}
