package olm

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/stretchr/testify/require"
)

func TestPermissionStatus(t *testing.T) {
	namespace := "ns"
	type gvkn struct {
		group   string
		version string
		kind    string
		name    string
	}
	tests := []struct {
		description                 string
		csv                         *v1alpha1.ClusterServiceVersion
		existingObjs                []runtime.Object
		met                         bool
		expectedRequirementStatuses map[gvkn]v1alpha1.RequirementStatus
	}{
		{
			description: "AllPermissionsMet",
			csv: csv("csv1",
				namespace,
				"",
				installStrategy(
					"csv1-dep",
					[]install.StrategyDeploymentPermissions{
						{
							ServiceAccountName: "sa",
							Rules: []rbacv1.PolicyRule{
								{
									APIGroups: []string{""},
									Verbs:     []string{"*"},
									Resources: []string{"donuts"},
								},
							},
						},
					},
					nil,
				),
				nil,
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs: []runtime.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sa",
						Namespace: namespace,
						UID:       types.UID("sa"),
					},
				},
				&rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "role",
						Namespace: namespace,
					},
					Rules: []rbacv1.PolicyRule{
						{
							APIGroups: []string{""},
							Verbs:     []string{"*"},
							Resources: []string{"donuts"},
						},
					},
				},
				&rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "roleBinding",
						Namespace: namespace,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      "sa",
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     "role",
					},
				},
			},
			met: true,
			expectedRequirementStatuses: map[gvkn]v1alpha1.RequirementStatus{
				{"", "v1", "ServiceAccount", "sa"}: {
					Group:   "",
					Version: "v1",
					Kind:    "ServiceAccount",
					Name:    "sa",
					Status:  v1alpha1.RequirementStatusReasonPresent,
					Dependents: []v1alpha1.DependentStatus{
						{
							Group:   "rbac.authorization.k8s.io",
							Kind:    "PolicyRule",
							Version: "v1beta1",
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			op, err := NewFakeOperator(nil, test.existingObjs, nil, nil, &install.StrategyResolver{}, namespace)
			require.NoError(t, err)

			// stopCh is closed when op.Run(...) exits
			stopCh := make(chan struct{})
			defer func() { stopCh <- struct{}{} }()

			t.Log("starting queue informer operator...")
			ready, _ := op.Run(stopCh)
			<-ready
			t.Log("queue informer operator ready")

			// get the permission status
			met, statuses := op.permissionStatus(test.csv)
			require.Equal(t, test.met, met)

			for _, status := range statuses {
				key := gvkn{
					group:   status.Group,
					version: status.Version,
					kind:    status.Kind,
					name:    status.Name,
				}

				expected, ok := test.expectedRequirementStatuses[key]
				require.True(t, ok, fmt.Sprintf("permission requirement status %+v found but not expected", key))
				require.Len(t, status.Dependents, len(expected.Dependents), "number of dependents is not what was expected")

				// delete the requirement status to mark as found
				delete(test.expectedRequirementStatuses, key)
			}

			require.Len(t, test.expectedRequirementStatuses, 0, "not all expected permission requirement statuses were found")

		})
	}
}
