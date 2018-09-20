package authorizer

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	v1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

func TestRuleSatisfied(t *testing.T) {

	namespace := "coffee-shop"

	csv := &v1alpha1.ClusterServiceVersion{}
	csv.SetName("barista-operator")
	csv.SetUID(types.UID("barista-operator"))

	sa := &corev1.ServiceAccount{}
	sa.SetName("barista-operator")
	sa.SetUID(types.UID("barista-operator"))

	tests := []struct {
		description          string
		rule                 rbacv1.PolicyRule
		existingRoles        []*rbacv1.Role
		existingRoleBindings []*rbacv1.RoleBinding
		expectedError        string
		satisfied            bool
	}{
		{
			description: "NotSatisfied",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{
					"",
				},
				Verbs: []string{
					"*",
				},
				Resources: []string{
					"donuts",
				},
			},
			satisfied: false,
		},
		{
			description: "SatisfiedBySingleRole",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{
					"",
				},
				Verbs: []string{
					"*",
				},
				Resources: []string{
					"donuts",
				},
			},
			existingRoles: []*rbacv1.Role{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "coffee",
						Namespace: namespace,
					},
					Rules: []rbacv1.PolicyRule{
						{
							APIGroups: []string{
								"",
							},
							Verbs: []string{
								"*",
							},
							Resources: []string{
								"donuts",
							},
						},
					},
				},
			},
			existingRoleBindings: []*rbacv1.RoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "coffee",
						Namespace: namespace,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     "coffee",
					},
				},
			},
			satisfied: true,
		},
		{
			description: "NotSatisfiedByRoleOwnerConflict",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{
					"",
				},
				Verbs: []string{
					"create",
					"update",
					"delete",
				},
				Resources: []string{
					"donuts",
				},
			},
			existingRoles: []*rbacv1.Role{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "coffee",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "v1alpha1",
								Kind:       "ClusterServiceVersion",
								Name:       csv.GetName(),
								UID:        csv.GetUID(),
							},
							{
								APIVersion: "v1alpha1",
								Kind:       "ClusterServiceVersion",
								Name:       "big-donut",
								UID:        types.UID("big-donut"),
							},
						},
					},
					Rules: []rbacv1.PolicyRule{
						{
							APIGroups: []string{
								"",
							},
							Verbs: []string{
								"create",
								"update",
							},
							Resources: []string{
								"donuts",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "napkin",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "v1alpha1",
								Kind:       "ClusterServiceVersion",
								Name:       "big-donut",
								UID:        types.UID("big-donut"),
							},
						},
					},
					Rules: []rbacv1.PolicyRule{
						{
							APIGroups: []string{
								"",
							},
							Verbs: []string{
								"delete",
							},
							Resources: []string{
								"donuts",
							},
						},
					},
				},
			},
			existingRoleBindings: []*rbacv1.RoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "coffee",
						Namespace: namespace,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     "coffee",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "napkin",
						Namespace: namespace,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     "napkin",
					},
				},
			},
			satisfied: false,
		},
		{
			description: "SatisfiedByRoleWithConcurrentOwners",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{
					"",
				},
				Verbs: []string{
					"create",
					"update",
					"delete",
				},
				Resources: []string{
					"donuts",
				},
			},
			existingRoles: []*rbacv1.Role{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "coffee",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "v1alpha1",
								Kind:       "ClusterServiceVersion",
								Name:       csv.GetName(),
								UID:        csv.GetUID(),
							},
							{
								APIVersion: "v1alpha1",
								Kind:       "ClusterServiceVersion",
								Name:       "big-donut",
								UID:        types.UID("big-donut"),
							},
						},
					},
					Rules: []rbacv1.PolicyRule{
						{
							APIGroups: []string{
								"",
							},
							Verbs: []string{
								"create",
								"update",
								"delete",
							},
							Resources: []string{
								"donuts",
							},
						},
					},
				},
			},
			existingRoleBindings: []*rbacv1.RoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "coffee",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "",
								Kind:       "ServiceAccount",
								Name:       "mixologist",
								UID:        types.UID("mixologist"),
							},
						},
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     "coffee",
					},
				},
			},
			satisfied: true,
		},
		{
			description: "SatisfiedByMutlipleRoles",
			rule: rbacv1.PolicyRule{
				APIGroups: []string{
					"",
				},
				Verbs: []string{
					"create",
					"update",
					"delete",
				},
				Resources: []string{
					"donuts",
				},
			},
			existingRoles: []*rbacv1.Role{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "coffee",
						Namespace: namespace,
					},
					Rules: []rbacv1.PolicyRule{
						{
							APIGroups: []string{
								"",
							},
							Verbs: []string{
								"create",
								"update",
							},
							Resources: []string{
								"donuts",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "napkin",
						Namespace: namespace,
					},
					Rules: []rbacv1.PolicyRule{
						{
							APIGroups: []string{
								"",
							},
							Verbs: []string{
								"delete",
							},
							Resources: []string{
								"donuts",
							},
						},
					},
				},
			},
			existingRoleBindings: []*rbacv1.RoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "coffee",
						Namespace: namespace,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     "coffee",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "napkin",
						Namespace: namespace,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     "napkin",
					},
				},
			},
			satisfied: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			// create existing objects
			k8sObjs := Objs(tt.existingRoles, tt.existingRoleBindings)

			// create the fake CSVAuthorizorClient
			authorizerClient, err := NewFakeCSVAuthorizerClient(k8sObjs, csv, namespace)
			require.NoError(t, err)

			// check if the rule is satisfied
			satisfied, err := authorizerClient.RuleSatisfied(sa, namespace, tt.rule)
			if tt.expectedError != "" {
				require.Error(t, err, "an error was expected")
				require.Equal(t, tt.expectedError, err.Error, "error did not match expected error")
			}
			require.Equal(t, tt.satisfied, satisfied)
		})
	}
}

func NewFakeCSVAuthorizerClient(k8sObjs []runtime.Object, csv *v1alpha1.ClusterServiceVersion, namespace string) (*CSVAuthorizerClient, error) {
	// create client fakes
	opClientFake := operatorclient.NewClient(k8sfake.NewSimpleClientset(k8sObjs...), apiextensionsfake.NewSimpleClientset(), apiregistrationfake.NewSimpleClientset())

	// create test namespace
	_, err := opClientFake.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
	if err != nil {
		return nil, err
	}

	authorizerClient := NewCSVAuthorizerClient(opClientFake, csv)

	return authorizerClient, nil

}

func Objs(roles []*rbacv1.Role, roleBindings []*rbacv1.RoleBinding) []runtime.Object {
	k8sObjs := make([]runtime.Object, 0, len(roles)+len(roleBindings))
	for _, role := range roles {
		k8sObjs = append(k8sObjs, role)
	}

	for _, roleBinding := range roleBindings {
		k8sObjs = append(k8sObjs, roleBinding)
	}

	return k8sObjs
}
