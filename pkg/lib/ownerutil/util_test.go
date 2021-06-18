package ownerutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func TestIsOwnedBy(t *testing.T) {
	return
}

func TestCSVOwnerSelector(t *testing.T) {
	csvType := metav1.TypeMeta{
		Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
		APIVersion: operatorsv1alpha1.GroupVersion,
	}

	tests := []struct {
		name string
		csv  *operatorsv1alpha1.ClusterServiceVersion
	}{
		{
			name: "CSV with name longer than 63 characters",
			csv: &operatorsv1alpha1.ClusterServiceVersion{
				TypeMeta: csvType,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "clusterkubedescheduleroperator.4.6.0-202106010807.p0.git.5db84c5",
					Namespace: "test-namespace",
				},
			},
		},
		{
			name: "CSV with invalid name",
			csv: &operatorsv1alpha1.ClusterServiceVersion{
				TypeMeta: csvType,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "something@somewhere",
					Namespace: "test-namespace",
				},
			},
		},
		{
			name: "CSV with empty string name",
			csv: &operatorsv1alpha1.ClusterServiceVersion{
				TypeMeta: csvType,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "",
					Namespace: "test-namespace",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := CSVOwnerSelector(tt.csv)

			assert.NotNil(t, selector)
			assert.False(t, selector.Empty())
		})
	}
}
