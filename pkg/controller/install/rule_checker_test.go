package install

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	v1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

func TestRuleSatisfied(t *testing.T) {

	csv := &v1alpha1.ClusterServiceVersion{}
	csv.SetName("barista-operator")
	csv.SetUID(types.UID("barista-operator"))

	sa := &corev1.ServiceAccount{}
	sa.SetNamespace("coffee-shop")
	sa.SetName("barista-operator")
	sa.SetUID(types.UID("barista-operator"))

	tests := []struct {
		description                 string
		namespace                   string
		rule                        rbacv1.PolicyRule
		existingRoles               []*rbacv1.Role
		existingRoleBindings        []*rbacv1.RoleBinding
		existingClusterRoles        []*rbacv1.ClusterRole
		existingClusterRoleBindings []*rbacv1.ClusterRoleBinding
		expectedError               string
		satisfied                   bool
	}{
		{
			description: "NotSatisfied",
			namespace:   "coffee-shop",
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
			namespace:   "coffee-shop",
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
						Namespace: "coffee-shop",
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
						Namespace: "coffee-shop",
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: sa.GetNamespace(),
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
			namespace:   "coffee-shop",
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
						Namespace: "coffee-shop",
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
						Namespace: "coffee-shop",
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
						Namespace: "coffee-shop",
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: sa.GetNamespace(),
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
						Namespace: "coffee-shop",
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: sa.GetNamespace(),
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
			namespace:   "coffee-shop",
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
						Namespace: "coffee-shop",
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
						Namespace: "coffee-shop",
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
							Namespace: sa.GetNamespace(),
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
			namespace:   "coffee-shop",
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
						Namespace: "coffee-shop",
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
						Namespace: "coffee-shop",
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
						Namespace: "coffee-shop",
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: "coffee-shop",
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
						Namespace: "coffee-shop",
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: sa.GetNamespace(),
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
		{
			description: "RuleSatisfiedByClusterRole",
			namespace:   metav1.NamespaceAll,
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
			existingClusterRoles: []*rbacv1.ClusterRole{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "coffee",
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
								"*",
							},
						},
					},
				},
			},
			existingClusterRoleBindings: []*rbacv1.ClusterRoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "coffee",
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: sa.GetNamespace(),
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "ClusterRole",
						Name:     "coffee",
					},
				},
			},
			satisfied: true,
		},
		{
			description: "RuleNotSatisfiedByClusterRole",
			namespace:   metav1.NamespaceAll,
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
			existingClusterRoles: []*rbacv1.ClusterRole{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "coffee",
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
								"*",
							},
						},
					},
				},
			},
			existingClusterRoleBindings: []*rbacv1.ClusterRoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "coffee",
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      sa.GetName(),
							Namespace: sa.GetNamespace(),
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "ClusterRole",
						Name:     "coffee",
					},
				},
			},
			satisfied: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			// create existing objects
			k8sObjs := Objs(tt.existingRoles,
				tt.existingRoleBindings,
				tt.existingClusterRoles,
				tt.existingClusterRoleBindings,
			)

			// create the fake CSVRuleChecker
			stopCh := make(chan struct{})
			defer func() { close(stopCh) }()

			t.Logf("calling NewFakeCSVRuleChecker...")
			ruleChecker, err := NewFakeCSVRuleChecker(k8sObjs, csv, tt.namespace, stopCh)
			require.NoError(t, err)
			t.Logf("NewFakeCSVRuleChecker returned")
			time.Sleep(1 * time.Second)

			t.Logf("checking if rules are satisfied...")
			// check if the rule is satisfied
			satisfied, err := ruleChecker.RuleSatisfied(sa, tt.namespace, tt.rule)
			if tt.expectedError != "" {
				require.Error(t, err, "an error was expected")
				require.Equal(t, tt.expectedError, err.Error, "error did not match expected error")
			}

			t.Logf("after checking if satisfied")
			require.Equal(t, tt.satisfied, satisfied)
		})
	}
}

func NewFakeCSVRuleChecker(k8sObjs []runtime.Object, csv *v1alpha1.ClusterServiceVersion, namespace string, stopCh <-chan struct{}) (*CSVRuleChecker, error) {
	// create client fakes
	opClientFake := operatorclient.NewClient(k8sfake.NewSimpleClientset(k8sObjs...), apiextensionsfake.NewSimpleClientset(), apiregistrationfake.NewSimpleClientset())

	// create test namespace
	if namespace != metav1.NamespaceAll {
		_, err := opClientFake.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
	}

	informerFactory := informers.NewSharedInformerFactory(opClientFake.KubernetesInterface(), 1*time.Second)
	roleInformer := informerFactory.Rbac().V1().Roles()
	roleBindingInformer := informerFactory.Rbac().V1().RoleBindings()
	clusterRoleInformer := informerFactory.Rbac().V1().ClusterRoles()
	clusterRoleBindingInformer := informerFactory.Rbac().V1().ClusterRoleBindings()

	// kick off informers
	for _, informer := range []cache.SharedIndexInformer{roleInformer.Informer(), roleBindingInformer.Informer(), clusterRoleInformer.Informer(), clusterRoleBindingInformer.Informer()} {
		go informer.Run(stopCh)

		synced := func() (bool, error) {
			return informer.HasSynced(), nil
		}

		// wait until the informer has synced to continue
		wait.PollUntil(500*time.Millisecond, synced, stopCh)
	}

	ruleChecker := NewCSVRuleChecker(roleInformer.Lister(), roleBindingInformer.Lister(), clusterRoleInformer.Lister(), clusterRoleBindingInformer.Lister(), csv)

	return ruleChecker, nil

}

func Objs(roles []*rbacv1.Role, roleBindings []*rbacv1.RoleBinding, clusterRoles []*rbacv1.ClusterRole, clusterRoleBindings []*rbacv1.ClusterRoleBinding) []runtime.Object {
	k8sObjs := make([]runtime.Object, 0, len(roles)+len(roleBindings)+len(clusterRoles)+len(clusterRoleBindings))
	for _, role := range roles {
		k8sObjs = append(k8sObjs, role)
	}

	for _, roleBinding := range roleBindings {
		k8sObjs = append(k8sObjs, roleBinding)
	}

	for _, clusterRole := range clusterRoles {
		k8sObjs = append(k8sObjs, clusterRole)
	}

	for _, clusterRoleBinding := range clusterRoleBindings {
		k8sObjs = append(k8sObjs, clusterRoleBinding)
	}

	return k8sObjs
}
