package resolver

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/require"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func TestGenerateName(t *testing.T) {
	type args struct {
		base string
		o    interface{}
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "generate",
			args: args{
				base: "myname",
				o: []string{"something"},
			},
			want: "myname-9c895f74f",
		},
		{
			name: "truncated",
			args: args{
				base: strings.Repeat("name", 100),
				o: []string{"something", "else"},
			},
			want: "namenamenamenamenamenamenamenamenamenamenamenamename-78fd8b4d6b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateName(tt.args.base, tt.args.o)
			require.Equal(t, tt.want, got)
			require.LessOrEqual(t, len(got), maxNameLength)
		})
	}
}

var runeSet = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-")

type validKubeName string

func (validKubeName) Generate(rand *rand.Rand, size int) reflect.Value {
	b := make([]rune, size)
	for i := range b {
		b[i] = runeSet[rand.Intn(len(runeSet))]
	}
	return reflect.ValueOf(validKubeName(b))
}

func TestGeneratesWithinRange(t *testing.T) {
	f := func(base validKubeName, o string) bool {
		return len(generateName(string(base), o)) <= maxNameLength
	}
	require.NoError(t, quick.Check(f, nil))
}

func TestRBACForClusterServiceVersion(t *testing.T) {
	serviceAccount1 := "test-service-account"
	serviceAccount2 := "second-service-account"
	csvName := "test-csv.v1.1.0"

	rules := []rbacv1.PolicyRule{
		{
			Verbs:     []string{"get"},
			APIGroups: []string{""},
			Resources: []string{"pods"},
		},
	}

	// Note: two CSVs have same name and permissions for a cluster role, this is chosen intentionally,
	// to verify that ClusterRole and ClusterRoleBinding have different names when the same CSV is installed
	// twice in the same cluster, but in different namespaces.
	tests := []struct {
		name string
		csv  v1alpha1.ClusterServiceVersion
		want map[string]*OperatorPermissions
	}{
		{
			name: "RoleBindings and one ClusterRoleBinding",
			csv: v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      csvName,
					Namespace: "test-namespace",
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName: v1alpha1.InstallStrategyNameDeployment,
						StrategySpec: v1alpha1.StrategyDetailsDeployment{
							Permissions: []v1alpha1.StrategyDeploymentPermissions{
								{
									ServiceAccountName: serviceAccount1,
									Rules:              rules,
								},
								{
									ServiceAccountName: serviceAccount1,
									Rules: []rbacv1.PolicyRule{
										{
											Verbs:     []string{"get"},
											APIGroups: []string{""},
											Resources: []string{"deployments"},
										},
									},
								},
								{
									ServiceAccountName: serviceAccount2,
									Rules:              rules,
								},
							},
							ClusterPermissions: []v1alpha1.StrategyDeploymentPermissions{
								{
									ServiceAccountName: serviceAccount1,
									Rules:              rules,
								},
							},
						},
					},
				},
			},
			want: map[string]*OperatorPermissions{
				serviceAccount1: {
					RoleBindings:        []*rbacv1.RoleBinding{{}, {}},
					ClusterRoleBindings: []*rbacv1.ClusterRoleBinding{{}},
				},
				serviceAccount2: {
					RoleBindings: []*rbacv1.RoleBinding{{}},
				},
			},
		},
		{
			name: "ClusterRoleBinding",
			csv: v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      csvName,
					Namespace: "second-namespace",
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName: v1alpha1.InstallStrategyNameDeployment,
						StrategySpec: v1alpha1.StrategyDetailsDeployment{
							ClusterPermissions: []v1alpha1.StrategyDeploymentPermissions{
								{
									ServiceAccountName: serviceAccount1,
									Rules:              rules,
								},
								{
									ServiceAccountName: serviceAccount2,
									Rules:              rules,
								},
							},
						},
					},
				},
			},
			want: map[string]*OperatorPermissions{
				serviceAccount1: {
					ClusterRoleBindings: []*rbacv1.ClusterRoleBinding{{}},
				},
				serviceAccount2: {
					ClusterRoleBindings: []*rbacv1.ClusterRoleBinding{{}},
				},
			},
		},
	}

	// declared here to verify that names are unique when same csv is install in different namespaces
	clusterRoleBindingNames := map[string]bool{}
	clusterRolesNames := map[string]bool{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RBACForClusterServiceVersion(&tt.csv)
			require.NoError(t, err)

			roleBindingNames := map[string]bool{}
			rolesNames := map[string]bool{}
			for serviceAccount, permissions := range tt.want {
				// Check that correct number of bindings is created
				require.Equal(t, len(permissions.RoleBindings), len(result[serviceAccount].RoleBindings))
				require.Equal(t, len(permissions.ClusterRoleBindings), len(result[serviceAccount].ClusterRoleBindings))

				// Check that testing ServiceAccount is the Subject of RoleBindings
				for _, roleBinding := range result[serviceAccount].RoleBindings {
					require.Len(t, roleBinding.Subjects, 1)
					require.Equal(t, serviceAccount, roleBinding.Subjects[0].Name)

					// Check that RoleBindings are created with unique names
					_, rbWithNameExists := roleBindingNames[roleBinding.Name]
					require.False(t, rbWithNameExists, "RoleBinding with the same name already generated")
					roleBindingNames[roleBinding.Name] = true
				}

				// Check that testing ServiceAccount is the Subject of ClusterrRoleBindings
				for _, clusterRoleBinding := range result[serviceAccount].ClusterRoleBindings {
					require.Len(t, clusterRoleBinding.Subjects, 1)
					require.Equal(t, serviceAccount, clusterRoleBinding.Subjects[0].Name)

					// Check that ClusterRoleBindings are created with unique names
					_, crbWithNameExists := clusterRoleBindingNames[clusterRoleBinding.Name]
					require.False(t, crbWithNameExists, "ClusterRoleBinding with the same name already generated")
					clusterRoleBindingNames[clusterRoleBinding.Name] = true
				}

				// Check that Roles are created with unique names
				for _, role := range result[serviceAccount].Roles {
					_, roleWithNameExists := rolesNames[role.Name]
					require.False(t, roleWithNameExists, "Role with the same name already generated")
					rolesNames[role.Name] = true
				}

				// Check that ClusterRoles are created with unique names
				for _, clusterRole := range result[serviceAccount].ClusterRoles {
					_, crWithNameExists := clusterRolesNames[clusterRole.Name]
					require.False(t, crWithNameExists, "ClusterRole with the same name already generated")
					clusterRolesNames[clusterRole.Name] = true
				}
			}
		})
	}
}
