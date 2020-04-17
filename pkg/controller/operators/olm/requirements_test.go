package olm

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
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
			description: "AllPermissionsMet",
			csv: csv("csv1",
				namespace,
				"0.0.0",
				"",
				installStrategy(
					"csv1-dep",
					[]v1alpha1.StrategyDeploymentPermissions{
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
					[]v1alpha1.StrategyDeploymentPermissions{
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
							Version: "v1",
						},
						{
							Group:   "rbac.authorization.k8s.io",
							Kind:    "PolicyRule",
							Version: "v1",
						},
					},
				},
				{"operators.coreos.com", "v1alpha1", "ClusterServiceVersion", "csv1"}: {
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "csv1",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
			},
			expectedError: nil,
		},
		{
			description: "OnePermissionNotMet",
			csv: csv("csv1",
				namespace,
				"0.0.0",
				"",
				installStrategy(
					"csv1-dep",
					[]v1alpha1.StrategyDeploymentPermissions{
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
					[]v1alpha1.StrategyDeploymentPermissions{
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
							Version: "v1",
						},
						{
							Group:   "rbac.authorization.k8s.io",
							Kind:    "PolicyRule",
							Version: "v1",
						},
					},
				},
				{"operators.coreos.com", "v1alpha1", "ClusterServiceVersion", "csv1"}: {
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "csv1",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
			},
			expectedError: nil,
		},
		{
			description: "AllRequirementsMet",
			csv: csv("csv1",
				namespace,
				"0.0.0",
				"",
				installStrategy(
					"csv1-dep",
					[]v1alpha1.StrategyDeploymentPermissions{
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
				[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
				[]*apiextensionsv1.CustomResourceDefinition{crd("c2", "v1", "g2")},
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
				crd("c1", "v1", "g1"),
				crd("c2", "v1", "g2"),
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
							Version: "v1",
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
							Version: "v1",
						},
					},
				},
				{"apiextensions.k8s.io", "v1", "CustomResourceDefinition", "c1.g1"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1.g1",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
				{"apiextensions.k8s.io", "v1", "CustomResourceDefinition", "c2.g2"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1",
					Kind:    "CustomResourceDefinition",
					Name:    "c2.g2",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
				{"operators.coreos.com", "v1alpha1", "ClusterServiceVersion", "csv1"}: {
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "csv1",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
			},
			expectedError: nil,
		},
		{
			description: "RequirementNotMet/NonServedCRDVersion",
			csv: csv("csv1",
				namespace,
				"0.0.0",
				"",
				installStrategy("csv1-dep", nil, nil),
				[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v2", "g1")},
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs: nil,
			existingExtObjs: []runtime.Object{
				crd("c1", "v1", "g1"),
			},
			met: false,
			expectedRequirementStatuses: map[gvkn]v1alpha1.RequirementStatus{
				{"apiextensions.k8s.io", "v1", "CustomResourceDefinition", "c1.g1"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1.g1",
					Status:  v1alpha1.RequirementStatusReasonNotPresent,
				},
				{"operators.coreos.com", "v1alpha1", "ClusterServiceVersion", "csv1"}: {
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "csv1",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
			},
			expectedError: nil,
		},
		{
			description: "RequirementNotMet/NotEstablishedCRDVersion",
			csv: csv("csv1",
				namespace,
				"0.0.0",
				"",
				installStrategy("csv1-dep", nil, nil),
				[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "version-not-found", "g1")},
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs: nil,
			existingExtObjs: []runtime.Object{
				crd("c1", "v2", "g1"),
			},
			met: false,
			expectedRequirementStatuses: map[gvkn]v1alpha1.RequirementStatus{
				{"apiextensions.k8s.io", "v1", "CustomResourceDefinition", "c1.g1"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1.g1",
					Status:  v1alpha1.RequirementStatusReasonNotAvailable,
				},
				{"operators.coreos.com", "v1alpha1", "ClusterServiceVersion", "csv1"}: {
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "csv1",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
			},
			expectedError: nil,
		},
		{
			description: "RequirementNotMet/NamesConflictedCRD",
			csv: csv("csv1",
				namespace,
				"0.0.0",
				"",
				installStrategy("csv1-dep", nil, nil),
				[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v2", "g1")},
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs: nil,
			existingExtObjs: []runtime.Object{
				func() *apiextensionsv1.CustomResourceDefinition {
					newCRD := crd("c1", "v2", "g1")
					// condition order: established, name accepted
					newCRD.Status.Conditions[0].Status = apiextensionsv1.ConditionTrue
					newCRD.Status.Conditions[1].Status = apiextensionsv1.ConditionFalse
					return newCRD
				}(),
			},
			met: false,
			expectedRequirementStatuses: map[gvkn]v1alpha1.RequirementStatus{
				{"apiextensions.k8s.io", "v1", "CustomResourceDefinition", "c1.g1"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1.g1",
					Status:  v1alpha1.RequirementStatusReasonNotAvailable,
				},
				{"operators.coreos.com", "v1alpha1", "ClusterServiceVersion", "csv1"}: {
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "csv1",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
			},
			expectedError: nil,
		},
		{
			description: "RequirementNotMet/CRDResourceInactive",
			csv: csv("csv1",
				namespace,
				"0.0.0",
				"",
				installStrategy("csv1-dep", nil, nil),
				[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v2", "g1")},
				nil,
				v1alpha1.CSVPhasePending,
			),
			existingObjs: nil,
			existingExtObjs: []runtime.Object{
				func() *apiextensionsv1.CustomResourceDefinition {
					newCRD := crd("c1", "v2", "g1")
					// condition order: established, name accepted
					newCRD.Status.Conditions[0].Status = apiextensionsv1.ConditionFalse
					newCRD.Status.Conditions[1].Status = apiextensionsv1.ConditionTrue
					return newCRD
				}(),
			},
			met: false,
			expectedRequirementStatuses: map[gvkn]v1alpha1.RequirementStatus{
				{"apiextensions.k8s.io", "v1", "CustomResourceDefinition", "c1.g1"}: {
					Group:   "apiextensions.k8s.io",
					Version: "v1",
					Kind:    "CustomResourceDefinition",
					Name:    "c1.g1",
					Status:  v1alpha1.RequirementStatusReasonNotAvailable,
				},
				{"operators.coreos.com", "v1alpha1", "ClusterServiceVersion", "csv1"}: {
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "csv1",
					Status:  v1alpha1.RequirementStatusReasonPresent,
				},
			},
			expectedError: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			op, err := NewFakeOperator(ctx, withNamespaces(namespace), withOperatorNamespace(namespace), withClientObjs(test.csv), withK8sObjs(test.existingObjs...), withExtObjs(test.existingExtObjs...))
			require.NoError(t, err)

			// Get the permission status
			met, statuses, err := op.requirementAndPermissionStatus(test.csv)

			if test.expectedError != nil {
				require.Error(t, err)
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

func TestMinKubeVersionStatus(t *testing.T) {
	namespace := "ns"
	csv := csv("csv1",
		namespace,
		"0.0.0",
		"",
		v1alpha1.NamedInstallStrategy{"deployment", v1alpha1.StrategyDetailsDeployment{}},
		nil,
		nil,
		v1alpha1.CSVPhasePending,
	)

	tests := []struct {
		description                 string
		csvName                     string
		minKubeVersion              string
		expectedMet                 bool
		expectedRequirementStatuses []v1alpha1.RequirementStatus
	}{
		{
			description:                 "minKubeVersion is not specfied",
			csvName:                     "test1",
			minKubeVersion:              "",
			expectedMet:                 true,
			expectedRequirementStatuses: []v1alpha1.RequirementStatus{},
		},
		{
			description:    "minKubeVersion is met",
			csvName:        "test2",
			minKubeVersion: "0.0.0",
			expectedMet:    true,
			expectedRequirementStatuses: []v1alpha1.RequirementStatus{
				v1alpha1.RequirementStatus{
					Status:  v1alpha1.RequirementStatusReasonPresent,
					Message: fmt.Sprintf("CSV minKubeVersion (%s) less than server version", "0.0.0"),
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "test2",
				},
			},
		},
		{
			description:    "minKubeVersion is unmet",
			csvName:        "test3",
			minKubeVersion: "999.999.999",
			expectedMet:    false,
			expectedRequirementStatuses: []v1alpha1.RequirementStatus{
				v1alpha1.RequirementStatus{
					Status:  v1alpha1.RequirementStatusReasonPresentNotSatisfied,
					Message: fmt.Sprintf("CSV version requirement not met: minKubeVersion (%s)", "999.999.999"),
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "test3",
				},
			},
		},
		{
			description:    "minKubeVersion is invalid",
			csvName:        "test4",
			minKubeVersion: "a.b.c",
			expectedMet:    false,
			expectedRequirementStatuses: []v1alpha1.RequirementStatus{
				v1alpha1.RequirementStatus{
					Status:  v1alpha1.RequirementStatusReasonPresentNotSatisfied,
					Message: "CSV version parsing error",
					Group:   "operators.coreos.com",
					Version: "v1alpha1",
					Kind:    "ClusterServiceVersion",
					Name:    "test4",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			op, err := NewFakeOperator(ctx, withNamespaces(namespace), withOperatorNamespace(namespace), withClientObjs(csv))
			require.NoError(t, err)

			// Get the permission status
			met, status := op.minKubeVersionStatus(test.csvName, test.minKubeVersion)
			require.Equal(t, test.expectedMet, met)
			if len(test.expectedRequirementStatuses) > 0 {
				require.Equal(t, status[0].Status, test.expectedRequirementStatuses[0].Status)
				require.Equal(t, status[0].Kind, test.expectedRequirementStatuses[0].Kind)
				require.Equal(t, status[0].Name, test.expectedRequirementStatuses[0].Name)
				require.Contains(t, status[0].Message, test.expectedRequirementStatuses[0].Message)
			} else {
				require.Equal(t, status, []v1alpha1.RequirementStatus(nil))
			}
		})
	}
}
