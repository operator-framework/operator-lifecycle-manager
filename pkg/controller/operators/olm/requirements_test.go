package olm

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
)

func TestRequirementAndPermissionStatus(t *testing.T) {
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
		existingExtObjs             []runtime.Object
		met                         bool
		expectedRequirementStatuses map[gvkn]v1alpha1.RequirementStatus
		expectedError               error
	}{
		{
			description: "BadInstallStrategy",
			csv: csv("csv1",
				namespace,
				"0.0",
				"",
				v1alpha1.NamedInstallStrategy{"deployment", json.RawMessage{}},
				nil,
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs:                nil,
			existingExtObjs:             nil,
			met:                         false,
			expectedRequirementStatuses: nil,
			expectedError:               fmt.Errorf("unexpected end of JSON input"),
		},
		{
			description: "AllPermissionsMet",
			csv: csv("csv1",
				namespace,
				"0.0",
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
					[]install.StrategyDeploymentPermissions{
						{
							ServiceAccountName: "sa",
							Rules: []rbacv1.PolicyRule{
								{
									Verbs:           []string{"get"},
									NonResourceURLs: []string{"/osbs"},
								},
							},
						},
					},
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
				&rbacv1.ClusterRole{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clusterRole",
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:           []string{"get"},
							NonResourceURLs: []string{"/osbs"},
						},
					},
				},
				&rbacv1.ClusterRoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clusterRoleBinding",
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
						Kind:     "ClusterRole",
						Name:     "clusterRole",
					},
				},
			},
			existingExtObjs: nil,
			met:             true,
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
						{
							Group:   "rbac.authorization.k8s.io",
							Kind:    "PolicyRule",
							Version: "v1beta1",
						},
					},
				},
			},
			expectedError: nil,
		},
		{
			description: "OnePermissionNotMet",
			csv: csv("csv1",
				namespace,
				"0.0",
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
					[]install.StrategyDeploymentPermissions{
						{
							ServiceAccountName: "sa",
							Rules: []rbacv1.PolicyRule{
								{
									Verbs:           []string{"get"},
									NonResourceURLs: []string{"/osbs"},
								},
							},
						},
					},
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
				&rbacv1.ClusterRole{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clusterRole",
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:           []string{"get"},
							NonResourceURLs: []string{"/osbs/*"},
						},
					},
				},
				&rbacv1.ClusterRoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: "clusterRoleBinding",
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
						Kind:     "ClusterRole",
						Name:     "clusterRole",
					},
				},
			},
			existingExtObjs: nil,
			met:             false,
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
						{
							Group:   "rbac.authorization.k8s.io",
							Kind:    "PolicyRule",
							Version: "v1beta1",
						},
					},
				},
			},
			expectedError: nil,
		},
		{
			description: "AllRequirementsMet",
			csv: csv("csv1",
				namespace,
				"0.0",
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
				[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
				[]*v1beta1.CustomResourceDefinition{crd("c2", "v1")},
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
			existingExtObjs: []runtime.Object{
				crd("c1", "v1"),
				crd("c2", "v1"),
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
				{"apiextensions.k8s.io", "v1beta1", "CustomResourceDefinition", "c1group"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1beta1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1group",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
				{"apiextensions.k8s.io", "v1beta1", "CustomResourceDefinition", "c2group"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1beta1",
					Kind:    "CustomResourceDefinition",
					Name:    "c2group",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
			},
			expectedError: nil,
		},
		{
			description: "RequirementNotMet/NonServedCRDVersion",
			csv: csv("csv1",
				namespace,
				"0.0",
				"",
				installStrategy("csv1-dep", nil, nil),
				[]*v1beta1.CustomResourceDefinition{crd("c1", "v2")},
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs: nil,
			existingExtObjs: []runtime.Object{
				crd("c1", "v1"),
			},
			met: false,
			expectedRequirementStatuses: map[gvkn]v1alpha1.RequirementStatus{
				{"apiextensions.k8s.io", "v1beta1", "CustomResourceDefinition", "c1group"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1beta1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1group",
					Status:  v1alpha1.RequirementStatusReasonNotPresent,
				},
			},
			expectedError: nil,
		},
		{
			description: "RequirementNotMet/NotEstablishedCRDVersion",
			csv: csv("csv1",
				namespace,
				"0.0",
				"",
				installStrategy("csv1-dep", nil, nil),
				[]*v1beta1.CustomResourceDefinition{crd("c1", "version-not-found")},
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs: nil,
			existingExtObjs: []runtime.Object{
				crd("c1", "v2"),
			},
			met: false,
			expectedRequirementStatuses: map[gvkn]v1alpha1.RequirementStatus{
				{"apiextensions.k8s.io", "v1beta1", "CustomResourceDefinition", "c1group"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1beta1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1group",
					Status:  v1alpha1.RequirementStatusReasonNotAvailable,
				},
			},
			expectedError: nil,
		},
		{
			description: "RequirementNotMet/NamesConflictedCRD",
			csv: csv("csv1",
				namespace,
				"0.0",
				"",
				installStrategy("csv1-dep", nil, nil),
				[]*v1beta1.CustomResourceDefinition{crd("c1", "v2")},
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs: nil,
			existingExtObjs: []runtime.Object{
				func() *v1beta1.CustomResourceDefinition {
					newCRD := crd("c1", "v2")
					// condition order: established, name accepted
					newCRD.Status.Conditions[0].Status = v1beta1.ConditionTrue
					newCRD.Status.Conditions[1].Status = v1beta1.ConditionFalse
					return newCRD
				}(),
			},
			met: false,
			expectedRequirementStatuses: map[gvkn]v1alpha1.RequirementStatus{
				{"apiextensions.k8s.io", "v1beta1", "CustomResourceDefinition", "c1group"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1beta1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1group",
					Status:  v1alpha1.RequirementStatusReasonNotAvailable,
				},
			},
			expectedError: nil,
		},
		{
			description: "RequirementNotMet/CRDResourceInactive",
			csv: csv("csv1",
				namespace,
				"0.0",
				"",
				installStrategy("csv1-dep", nil, nil),
				[]*v1beta1.CustomResourceDefinition{crd("c1", "v2")},
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs: nil,
			existingExtObjs: []runtime.Object{
				func() *v1beta1.CustomResourceDefinition {
					newCRD := crd("c1", "v2")
					// condition order: established, name accepted
					newCRD.Status.Conditions[0].Status = v1beta1.ConditionFalse
					newCRD.Status.Conditions[1].Status = v1beta1.ConditionTrue
					return newCRD
				}(),
			},
			met: false,
			expectedRequirementStatuses: map[gvkn]v1alpha1.RequirementStatus{
				{"apiextensions.k8s.io", "v1beta1", "CustomResourceDefinition", "c1group"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1beta1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1group",
					Status:  v1alpha1.RequirementStatusReasonNotAvailable,
				},
			},
			expectedError: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			namespaceObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}
			test.existingObjs = append(test.existingObjs, namespaceObj)
			stopCh := make(chan struct{})
			defer func() { stopCh <- struct{}{} }()
			op, _, err := NewFakeOperator([]runtime.Object{test.csv}, test.existingObjs, test.existingExtObjs, nil, &install.StrategyResolver{}, []string{namespace}, stopCh)
			require.NoError(t, err)

			// Get the permission status
			met, statuses, err := op.requirementAndPermissionStatus(test.csv)
			if test.expectedError != nil {
				require.EqualError(t, test.expectedError, err.Error())
			}
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

				// Delete the requirement status to mark as found
				delete(test.expectedRequirementStatuses, key)
			}

			require.Len(t, test.expectedRequirementStatuses, 0, "not all expected permission requirement statuses were found")
		})
	}
}
