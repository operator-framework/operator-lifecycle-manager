package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	apiregv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsfake "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func TestCrossOwnerReferenceRemoval(t *testing.T) {
	clusterScopedKind := "ClusterScoped"

	type fields struct {
		kind          string
		uidNamespaces map[types.UID]string
	}
	type args struct {
		obj metav1.Object
	}
	type expected struct {
		obj     metav1.Object
		mutated bool
	}
	tests := []struct {
		description string
		fields      fields
		args        args
		expected    expected
	}{
		{
			description: "OneRef/NoResources/Removed",
			fields: fields{
				kind:          v1alpha1.ClusterServiceVersionKind,
				uidNamespaces: nil,
			},
			args: args{
				obj: &metav1.ObjectMeta{
					Namespace: "ns-a",
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a-uid",
						},
					},
				},
			},
			expected: expected{
				obj: &metav1.ObjectMeta{
					Namespace:       "ns-a",
					OwnerReferences: nil,
				},
				mutated: true,
			},
		},
		{
			description: "OneRef/ResourceMissing/Removed",
			fields: fields{
				kind: v1alpha1.ClusterServiceVersionKind,
				uidNamespaces: map[types.UID]string{
					"csv-a0-uid": "ns-a",
					"csv-a1-uid": "ns-a",
					"csv-b-uid":  "ns-b",
					"csv-c0-uid": "ns-c",
					"csv-c1-uid": "ns-c",
				},
			},
			args: args{
				obj: &metav1.ObjectMeta{
					Namespace: "ns-a",
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a-uid",
						},
					},
				},
			},
			expected: expected{
				obj: &metav1.ObjectMeta{
					Namespace:       "ns-a",
					OwnerReferences: nil,
				},
				mutated: true,
			},
		},
		{
			description: "OneRef/NotOfKind/Ignored",
			fields: fields{
				kind: v1alpha1.ClusterServiceVersionKind,
				uidNamespaces: map[types.UID]string{
					"csv-a0-uid": "ns-a",
					"csv-a1-uid": "ns-a",
					"csv-b-uid":  "ns-b",
					"csv-c0-uid": "ns-c",
					"csv-c1-uid": "ns-c",
				},
			},
			args: args{
				obj: &metav1.ObjectMeta{
					Namespace: "ns-a",
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: "DefinitelyNotACSV",
							UID:  "no-csv-here-uid",
						},
					},
				},
			},
			expected: expected{
				obj: &metav1.ObjectMeta{
					Namespace: "ns-a",
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: "DefinitelyNotACSV",
							UID:  "no-csv-here-uid",
						},
					},
				},
				mutated: false,
			},
		},
		{
			description: "OneRef/Internamespace/Removed",
			fields: fields{
				kind: v1alpha1.ClusterServiceVersionKind,
				uidNamespaces: map[types.UID]string{
					"csv-b-uid": "ns-b",
				},
			},
			args: args{
				obj: &metav1.ObjectMeta{
					Namespace: "ns-a",
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-b-uid",
						},
					},
				},
			},
			expected: expected{
				obj: &metav1.ObjectMeta{
					Namespace:       "ns-a",
					OwnerReferences: nil,
				},
				mutated: true,
			},
		},
		{
			description: "ManyRefs/Internamespace/Removed",
			fields: fields{
				kind: v1alpha1.ClusterServiceVersionKind,
				uidNamespaces: map[types.UID]string{
					"csv-a0-uid": "ns-a",
					"csv-a1-uid": "ns-a",
					"csv-b-uid":  "ns-b",
					"csv-c0-uid": "ns-c",
					"csv-c1-uid": "ns-c",
				},
			},
			args: args{
				obj: &metav1.ObjectMeta{
					Namespace: "ns-a",
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a0-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a1-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-c1-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-b-uid",
						},
					},
				},
			},
			expected: expected{
				obj: &metav1.ObjectMeta{
					Namespace: "ns-a",
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a0-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a1-uid",
						},
					},
				},
				mutated: true,
			},
		},
		{
			description: "OneRef/ClusterToNamespaced/Removed",
			fields: fields{
				kind: v1alpha1.ClusterServiceVersionKind,
				uidNamespaces: map[types.UID]string{
					"csv-a-uid": "ns-a",
				},
			},
			args: args{
				obj: &metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a-uid",
						},
					},
				},
			},
			expected: expected{
				obj: &metav1.ObjectMeta{
					OwnerReferences: nil,
				},
				mutated: true,
			},
		},
		{
			description: "ManyRefs/ClusterToNamespaced/Removed/NotOfKind/Ignored",
			fields: fields{
				kind: v1alpha1.ClusterServiceVersionKind,
				uidNamespaces: map[types.UID]string{
					"csv-a0-uid": "ns-a",
					"csv-a1-uid": "ns-a",
					"csv-b-uid":  "ns-b",
					"csv-c0-uid": "ns-c",
					"csv-c1-uid": "ns-c",
				},
			},
			args: args{
				obj: &metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a0-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a1-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a1-uid",
						},
						{
							Kind: "DefinitelyNotACSV",
							UID:  "no-csv-here-uid",
						},
					},
				},
			},
			expected: expected{
				obj: &metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: "DefinitelyNotACSV",
							UID:  "no-csv-here-uid",
						},
					},
				},
				mutated: true,
			},
		},
		{
			description: "ManyRefs/NamespacedToCluster/Kept/NotOfKind/Ignored",
			fields: fields{
				kind: clusterScopedKind,
				uidNamespaces: map[types.UID]string{
					"csk-0-uid": metav1.NamespaceAll,
					"csk-1-uid": metav1.NamespaceAll,
				},
			},
			args: args{
				obj: &metav1.ObjectMeta{
					Namespace: "ns-a",
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: clusterScopedKind,
							UID:  "csk-0-uid",
						},
						{
							Kind: clusterScopedKind,
							UID:  "csk-1-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-b-uid",
						},
					},
				},
			},
			expected: expected{
				obj: &metav1.ObjectMeta{
					Namespace: "ns-a",
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: clusterScopedKind,
							UID:  "csk-0-uid",
						},
						{
							Kind: clusterScopedKind,
							UID:  "csk-1-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-a-uid",
						},
						{
							Kind: v1alpha1.ClusterServiceVersionKind,
							UID:  "csv-b-uid",
						},
					},
				},
				mutated: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			removeBadRefs := crossNamespaceOwnerReferenceRemoval(tt.fields.kind, tt.fields.uidNamespaces)
			obj := tt.args.obj
			require.Equal(t, tt.expected.mutated, removeBadRefs(obj))
			require.Equal(t, tt.expected.obj, obj)
		})
	}
}

func TestCleanupOwnerReferences(t *testing.T) {
	clusterRoleKind := "ClusterRole"

	type fields struct {
		k8sObjs     []runtime.Object
		clientObjs  []runtime.Object
		apiServices []runtime.Object
	}
	type expected struct {
		err                 error
		csvs                []v1alpha1.ClusterServiceVersion
		clusterRoles        []rbacv1.ClusterRole
		clusterRoleBindings []rbacv1.ClusterRoleBinding
		roles               []rbacv1.Role
		roleBindings        []rbacv1.RoleBinding
		apiServices         []apiregv1.APIService
	}
	tests := []struct {
		description string
		fields      fields
		expected    expected
	}{
		{
			description: "CrossNamespace/Removed",
			fields: fields{
				k8sObjs: []runtime.Object{
					newClusterRole("cr", "cr-uid", newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-b-uid")),
					newClusterRoleBinding("crb", "crb-uid", newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-b-uid")),
					newRoleBinding("ns-a", "rb", "rb-a-uid", newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-b-uid")),
					newRole("ns-a", "r", "r-a-uid", newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-b-uid")),
				},
				clientObjs: []runtime.Object{
					newClusterServiceVersion("ns-b", "csv-b", "csv-b-uid"),
					newClusterServiceVersion("ns-a", "csv-a", "csv-a-uid", newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-b-uid")),
				},
				apiServices: []runtime.Object{
					newAPIService("apisvc", "apisvc-a-uid", nil, newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-b-uid")),
				},
			},
			expected: expected{
				csvs: []v1alpha1.ClusterServiceVersion{
					*newClusterServiceVersion("ns-a", "csv-a", "csv-a-uid"),
					*newClusterServiceVersion("ns-b", "csv-b", "csv-b-uid"),
				},
				clusterRoles: []rbacv1.ClusterRole{
					*newClusterRole("cr", "cr-uid"),
				},
				clusterRoleBindings: []rbacv1.ClusterRoleBinding{
					*newClusterRoleBinding("crb", "crb-uid"),
				},
				roles: []rbacv1.Role{
					*newRole("ns-a", "r", "r-a-uid"),
				},
				roleBindings: []rbacv1.RoleBinding{
					*newRoleBinding("ns-a", "rb", "rb-a-uid"),
				},
				apiServices: []apiregv1.APIService{
					*newAPIService("apisvc", "apisvc-a-uid", nil),
				},
			},
		},
		{
			description: "CrossNamespace/Removed/InNamespace/Kept",
			fields: fields{
				k8sObjs: []runtime.Object{
					newRoleBinding("ns-a", "rb", "rb-a-uid",
						newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-a-uid"),
						newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-b-uid"),
					),
				},
				clientObjs: []runtime.Object{
					newClusterServiceVersion("ns-a", "csv-a", "csv-a-uid"),
					newClusterServiceVersion("ns-b", "csv-b", "csv-b-uid"),
				},
			},
			expected: expected{
				csvs: []v1alpha1.ClusterServiceVersion{
					*newClusterServiceVersion("ns-a", "csv-a", "csv-a-uid"),
					*newClusterServiceVersion("ns-b", "csv-b", "csv-b-uid"),
				},
				roleBindings: []rbacv1.RoleBinding{
					*newRoleBinding("ns-a", "rb", "rb-a-uid", newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-a-uid")),
				},
			},
		},
		{
			description: "CrossNamespace/Removed/ToClusterScoped/Ignored",
			fields: fields{
				k8sObjs: []runtime.Object{
					newClusterRole("cr", "cr-uid"),
					newRoleBinding("ns-a", "rb", "rb-a-uid",
						newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-a-uid"),
						newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-b-uid"),
						newOwnerReference(clusterRoleKind, "cr-uid"),
					),
				},
				clientObjs: []runtime.Object{
					newClusterServiceVersion("ns-a", "csv-a", "csv-a-uid"),
					newClusterServiceVersion("ns-b", "csv-b", "csv-b-uid"),
				},
			},
			expected: expected{
				csvs: []v1alpha1.ClusterServiceVersion{
					*newClusterServiceVersion("ns-a", "csv-a", "csv-a-uid"),
					*newClusterServiceVersion("ns-b", "csv-b", "csv-b-uid"),
				},
				clusterRoles: []rbacv1.ClusterRole{
					*newClusterRole("cr", "cr-uid"),
				},
				roleBindings: []rbacv1.RoleBinding{
					*newRoleBinding("ns-a", "rb", "rb-a-uid",
						newOwnerReference(v1alpha1.ClusterServiceVersionKind, "csv-a-uid"),
						newOwnerReference(clusterRoleKind, "cr-uid"),
					),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			k8sClient := k8sfake.NewSimpleClientset(tt.fields.k8sObjs...)
			c := operatorclient.NewClient(k8sClient, apiextensionsfake.NewSimpleClientset(), apiregistrationfake.NewSimpleClientset(tt.fields.apiServices...))
			crc := operatorsfake.NewSimpleClientset(tt.fields.clientObjs...)
			require.Equal(t, tt.expected.err, cleanupOwnerReferences(c, crc))

			listOpts := metav1.ListOptions{}
			csvs, err := crc.OperatorsV1alpha1().ClusterServiceVersions(metav1.NamespaceAll).List(context.TODO(), listOpts)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expected.csvs, csvs.Items)

			clusterRoles, err := c.KubernetesInterface().RbacV1().ClusterRoles().List(context.TODO(), listOpts)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expected.clusterRoles, clusterRoles.Items)

			clusterRoleBindings, err := c.KubernetesInterface().RbacV1().ClusterRoleBindings().List(context.TODO(), listOpts)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expected.clusterRoleBindings, clusterRoleBindings.Items)

			roles, err := c.KubernetesInterface().RbacV1().Roles(metav1.NamespaceAll).List(context.TODO(), listOpts)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expected.roles, roles.Items)

			roleBindings, err := c.KubernetesInterface().RbacV1().RoleBindings(metav1.NamespaceAll).List(context.TODO(), listOpts)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expected.roleBindings, roleBindings.Items)

			apiService, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().List(context.TODO(), listOpts)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expected.apiServices, apiService.Items)
		})
	}
}

func TestCheckAPIServiceLabels(t *testing.T) {
	type fields struct {
		apiServices []runtime.Object
	}

	type expected struct {
		err         error
		apiServices []apiregv1.APIService
	}

	tests := []struct {
		description string
		fields      fields
		expected    expected
	}{
		{
			description: "NoLabel/UpdateAPIService",
			fields: fields{
				apiServices: []runtime.Object{
					newAPIService("v1.packages.operators.coreos.com", "apisvc-a-uid", map[string]string{}),
				},
			},
			expected: expected{
				apiServices: []apiregv1.APIService{
					*newAPIService("v1.packages.operators.coreos.com", "apisvc-a-uid", map[string]string{ownerutil.OwnerKey: ownerutil.OwnerPackageServer}),
				},
			},
		},
		{
			description: "WrongLabel/UpdateAPIService",
			fields: fields{
				apiServices: []runtime.Object{
					newAPIService("v1.packages.operators.coreos.com", "apisvc-a-uid", map[string]string{ownerutil.OwnerKey: "banana"}),
				},
			},
			expected: expected{
				apiServices: []apiregv1.APIService{
					*newAPIService("v1.packages.operators.coreos.com", "apisvc-a-uid", map[string]string{ownerutil.OwnerKey: ownerutil.OwnerPackageServer}),
				},
			},
		},
		{
			description: "CorrectLabel/NoUpdate",
			fields: fields{
				apiServices: []runtime.Object{
					newAPIService("v1.packages.operators.coreos.com", "apisvc-a-uid", map[string]string{ownerutil.OwnerKey: ownerutil.OwnerPackageServer}),
				},
			},
			expected: expected{
				apiServices: []apiregv1.APIService{
					*newAPIService("v1.packages.operators.coreos.com", "apisvc-a-uid", map[string]string{ownerutil.OwnerKey: ownerutil.OwnerPackageServer}),
				},
			},
		},
		{
			description: "WrongAPIService/NoUpdate",
			fields: fields{
				apiServices: []runtime.Object{
					newAPIService("banana", "apisvc-a-uid", map[string]string{ownerutil.OwnerKey: "banana"}),
				},
			},
			expected: expected{
				apiServices: []apiregv1.APIService{
					*newAPIService("banana", "apisvc-a-uid", map[string]string{ownerutil.OwnerKey: "banana"}),
				},
			},
		},
		{
			description: "NoLabels/Update",
			fields: fields{
				apiServices: []runtime.Object{
					newAPIService("v1.packages.operators.coreos.com", "apisvc-a-uid", nil),
				},
			},
			expected: expected{
				apiServices: []apiregv1.APIService{
					*newAPIService("v1.packages.operators.coreos.com", "apisvc-a-uid", map[string]string{ownerutil.OwnerKey: ownerutil.OwnerPackageServer}),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			listOpts := metav1.ListOptions{}
			k8sClient := k8sfake.NewSimpleClientset()
			c := operatorclient.NewClient(k8sClient, apiextensionsfake.NewSimpleClientset(), apiregistrationfake.NewSimpleClientset(tt.fields.apiServices...))
			require.Equal(t, tt.expected.err, ensureAPIServiceLabels(c.ApiregistrationV1Interface()))

			apiService, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().List(context.TODO(), listOpts)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expected.apiServices, apiService.Items)
		})
	}

}

func newOwnerReference(kind string, uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{
		Kind: kind,
		UID:  uid,
	}
}

func newClusterServiceVersion(namespace, name string, uid types.UID, ownerRefs ...metav1.OwnerReference) *v1alpha1.ClusterServiceVersion {
	csv := &v1alpha1.ClusterServiceVersion{}
	csv.SetUID(uid)
	csv.SetNamespace(namespace)
	csv.SetName(name)
	csv.SetUID(uid)
	csv.SetOwnerReferences(ownerRefs)
	return csv
}

func newClusterRole(name string, uid types.UID, ownerRefs ...metav1.OwnerReference) *rbacv1.ClusterRole {
	clusterRole := &rbacv1.ClusterRole{}
	clusterRole.SetUID(uid)
	clusterRole.SetName(name)
	clusterRole.SetOwnerReferences(ownerRefs)
	return clusterRole
}

func newClusterRoleBinding(name string, uid types.UID, ownerRefs ...metav1.OwnerReference) *rbacv1.ClusterRoleBinding {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
	clusterRoleBinding.SetUID(uid)
	clusterRoleBinding.SetName(name)
	clusterRoleBinding.SetOwnerReferences(ownerRefs)
	return clusterRoleBinding
}

func newRole(namespace, name string, uid types.UID, ownerRefs ...metav1.OwnerReference) *rbacv1.Role {
	role := &rbacv1.Role{}
	role.SetUID(uid)
	role.SetNamespace(namespace)
	role.SetName(name)
	role.SetOwnerReferences(ownerRefs)
	return role
}

func newRoleBinding(namespace, name string, uid types.UID, ownerRefs ...metav1.OwnerReference) *rbacv1.RoleBinding {
	roleBinding := &rbacv1.RoleBinding{}
	roleBinding.SetUID(uid)
	roleBinding.SetNamespace(namespace)
	roleBinding.SetName(name)
	roleBinding.SetOwnerReferences(ownerRefs)
	return roleBinding
}

func newAPIService(name string, uid types.UID, labels map[string]string, ownerRefs ...metav1.OwnerReference) *apiregv1.APIService {
	apiService := &apiregv1.APIService{}
	apiService.SetUID(uid)
	apiService.SetName(name)
	apiService.SetOwnerReferences(ownerRefs)
	apiService.SetLabels(labels)
	return apiService
}
