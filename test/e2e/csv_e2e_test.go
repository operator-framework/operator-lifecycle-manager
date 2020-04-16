package e2e

import (
	"context"
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"k8s.io/apimachinery/pkg/util/intstr"
	"strconv"
	"strings"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	. "github.com/onsi/ginkgo"
	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

var _ = Describe("CSV", func() {
	It("create with unmet requirements mini kube version", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "999.999.999",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
			},
		}

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvPendingChecker)
		require.NoError(GinkgoT(), err)

		// Shouldn't create deployment
		_, err = c.GetDeployment(testNamespace, depName)
		require.Error(GinkgoT(), err)
	})
	// TODO: same test but missing serviceaccount instead
	It("create with unmet requirements CRD", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							DisplayName: "Not In Cluster",
							Description: "A CRD that is not currently in the cluster",
							Name:        "not.in.cluster.com",
							Version:     "v1alpha1",
							Kind:        "NotInCluster",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvPendingChecker)
		require.NoError(GinkgoT(), err)

		// Shouldn't create deployment
		_, err = c.GetDeployment(testNamespace, depName)
		require.Error(GinkgoT(), err)
	})
	It("create with unmet permissions CRD", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		saName := genName("dep-")
		permissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"create"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: crdName,
						},
					},
				},
			},
		}

		// Create dependency first (CRD)
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, true, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvPendingChecker)
		require.NoError(GinkgoT(), err)

		// Shouldn't create deployment
		_, err = c.GetDeployment(testNamespace, depName)
		require.Error(GinkgoT(), err)
	})
	It("create with unmet requirements API service", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Required: []v1alpha1.APIServiceDescription{
						{
							DisplayName: "Not In Cluster",
							Description: "An apiservice that is not currently in the cluster",
							Group:       "not.in.cluster.com",
							Version:     "v1alpha1",
							Kind:        "NotInCluster",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvPendingChecker)
		require.NoError(GinkgoT(), err)

		// Shouldn't create deployment
		_, err = c.GetDeployment(testNamespace, depName)
		require.Error(GinkgoT(), err)
	})
	It("create with unmet permissions API service", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		saName := genName("dep-")
		permissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"create"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: saName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
				// Cheating a little; this is an APIservice that will exist for the e2e tests
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Required: []v1alpha1.APIServiceDescription{
						{
							Group:       "packages.operators.coreos.com",
							Version:     "v1",
							Kind:        "PackageManifest",
							DisplayName: "Package Manifest",
							Description: "An apiservice that exists",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvPendingChecker)
		require.NoError(GinkgoT(), err)

		// Shouldn't create deployment
		_, err = c.GetDeployment(testNamespace, depName)
		require.Error(GinkgoT(), err)
	})
	It("create with unmet requirements native API", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
				NativeAPIs:      []metav1.GroupVersionKind{{Group: "kubenative.io", Version: "v1", Kind: "Native"}},
			},
		}

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvPendingChecker)
		require.NoError(GinkgoT(), err)

		// Shouldn't create deployment
		_, err = c.GetDeployment(testNamespace, depName)
		require.Error(GinkgoT(), err)
	})
	// TODO: same test but create serviceaccount instead
	It("create requirements met CRD", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		sa := corev1.ServiceAccount{}
		sa.SetName(genName("sa-"))
		sa.SetNamespace(testNamespace)
		_, err := c.CreateServiceAccount(&sa)
		require.NoError(GinkgoT(), err, "could not create ServiceAccount %#v", sa)

		permissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: sa.GetName(),
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"create"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: sa.GetName(),
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
					{
						Verbs:           []string{"put", "post", "get"},
						NonResourceURLs: []string{"/osb", "/osb/*"},
					},
				},
			},
		}

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: crdName,
						},
					},
				},
			},
		}

		// Create CSV first, knowing it will fail
		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, true, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvPendingChecker)
		require.NoError(GinkgoT(), err)

		crd := apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		}
		crd.SetOwnerReferences([]metav1.OwnerReference{{
			Name:       fetchedCSV.GetName(),
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			Kind:       v1alpha1.ClusterServiceVersionKind,
			UID:        fetchedCSV.GetUID(),
		}})
		cleanupCRD, err := createCRD(c, crd)
		defer cleanupCRD()
		require.NoError(GinkgoT(), err)

		// Create Role/Cluster Roles and RoleBindings
		role := rbacv1.Role{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		role.SetName(genName("dep-"))
		role.SetNamespace(testNamespace)
		_, err = c.CreateRole(&role)
		require.NoError(GinkgoT(), err, "could not create Role")

		roleBinding := rbacv1.RoleBinding{
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
				Name:     role.GetName(),
			},
		}
		roleBinding.SetName(genName("dep-"))
		roleBinding.SetNamespace(testNamespace)
		_, err = c.CreateRoleBinding(&roleBinding)
		require.NoError(GinkgoT(), err, "could not create RoleBinding")

		clusterRole := rbacv1.ClusterRole{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		clusterRole.SetName(genName("dep-"))
		_, err = c.CreateClusterRole(&clusterRole)
		require.NoError(GinkgoT(), err, "could not create ClusterRole")

		nonResourceClusterRole := rbacv1.ClusterRole{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:           []string{"put", "post", "get"},
					NonResourceURLs: []string{"/osb", "/osb/*"},
				},
			},
		}
		nonResourceClusterRole.SetName(genName("dep-"))
		_, err = c.CreateClusterRole(&nonResourceClusterRole)
		require.NoError(GinkgoT(), err, "could not create ClusterRole")

		clusterRoleBinding := rbacv1.ClusterRoleBinding{
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
				Name:     clusterRole.GetName(),
			},
		}
		clusterRoleBinding.SetName(genName("dep-"))
		_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
		require.NoError(GinkgoT(), err, "could not create ClusterRoleBinding")

		nonResourceClusterRoleBinding := rbacv1.ClusterRoleBinding{
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
				Name:     nonResourceClusterRole.GetName(),
			},
		}
		nonResourceClusterRoleBinding.SetName(genName("dep-"))
		_, err = c.CreateClusterRoleBinding(&nonResourceClusterRoleBinding)
		require.NoError(GinkgoT(), err, "could not create ClusterRoleBinding")

		fmt.Println("checking for deployment")
		// Poll for deployment to be ready
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			dep, err := c.GetDeployment(testNamespace, depName)
			if k8serrors.IsNotFound(err) {
				fmt.Printf("deployment %s not found\n", depName)
				return false, nil
			} else if err != nil {
				fmt.Printf("unexpected error fetching deployment %s\n", depName)
				return false, err
			}
			if dep.Status.UpdatedReplicas == *(dep.Spec.Replicas) &&
				dep.Status.Replicas == *(dep.Spec.Replicas) &&
				dep.Status.AvailableReplicas == *(dep.Spec.Replicas) {
				fmt.Println("deployment ready")
				return true, nil
			}
			fmt.Println("deployment not ready")
			return false, nil
		})
		require.NoError(GinkgoT(), err)

		fetchedCSV, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Delete CRD
		cleanupCRD()

		// Wait for CSV failure
		fetchedCSV, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvPendingChecker)
		require.NoError(GinkgoT(), err)

		// Recreate the CRD
		cleanupCRD, err = createCRD(c, crd)
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		// Wait for CSV success again
		fetchedCSV, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
	})
	It("create requirements met API service", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		sa := corev1.ServiceAccount{}
		sa.SetName(genName("sa-"))
		sa.SetNamespace(testNamespace)
		_, err := c.CreateServiceAccount(&sa)
		require.NoError(GinkgoT(), err, "could not create ServiceAccount")

		permissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: sa.GetName(),
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"create"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: sa.GetName(),
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
				// Cheating a little; this is an APIservice that will exist for the e2e tests
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Required: []v1alpha1.APIServiceDescription{
						{
							Group:       "packages.operators.coreos.com",
							Version:     "v1",
							Kind:        "PackageManifest",
							DisplayName: "Package Manifest",
							Description: "An apiservice that exists",
						},
					},
				},
			},
		}

		// Create Role/Cluster Roles and RoleBindings
		role := rbacv1.Role{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		role.SetName(genName("dep-"))
		role.SetNamespace(testNamespace)
		_, err = c.CreateRole(&role)
		require.NoError(GinkgoT(), err, "could not create Role")

		roleBinding := rbacv1.RoleBinding{
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
				Name:     role.GetName(),
			},
		}
		roleBinding.SetName(genName("dep-"))
		roleBinding.SetNamespace(testNamespace)
		_, err = c.CreateRoleBinding(&roleBinding)
		require.NoError(GinkgoT(), err, "could not create RoleBinding")

		clusterRole := rbacv1.ClusterRole{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		clusterRole.SetName(genName("dep-"))
		_, err = c.CreateClusterRole(&clusterRole)
		require.NoError(GinkgoT(), err, "could not create ClusterRole")

		clusterRoleBinding := rbacv1.ClusterRoleBinding{
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
				Name:     clusterRole.GetName(),
			},
		}
		clusterRoleBinding.SetName(genName("dep-"))
		_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
		require.NoError(GinkgoT(), err, "could not create ClusterRoleBinding")

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		compareResources(GinkgoT(), fetchedCSV, sameCSV)
	})
	It("create with owned API service", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fez", "fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSV for the package-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName(depName)

		// Create the APIService CSV
		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer func() {
			watcher, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Watch(context.TODO(), metav1.ListOptions{FieldSelector: "metadata.name=" + apiServiceName})
			require.NoError(GinkgoT(), err)

			deleted := make(chan struct{})
			go func() {
				events := watcher.ResultChan()
				for {
					select {
					case evt := <-events:
						if evt.Type == watch.Deleted {
							deleted <- struct{}{}
							return
						}
					case <-time.After(pollDuration):
						require.FailNow(GinkgoT(), "apiservice not cleaned up after CSV deleted")
					}
				}
			}()

			cleanupCSV()
			<-deleted
		}()

		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should create Deployment
		dep, err := c.GetDeployment(testNamespace, depName)
		require.NoError(GinkgoT(), err, "error getting expected Deployment")

		// Should create APIService
		apiService, err := c.GetAPIService(apiServiceName)
		require.NoError(GinkgoT(), err, "error getting expected APIService")

		// Should create Service
		serviceName := fmt.Sprintf("%s-service", depName)
		_, err = c.GetService(testNamespace, serviceName)
		require.NoError(GinkgoT(), err, "error getting expected Service")

		// Should create certificate Secret
		secretName := fmt.Sprintf("%s-cert", serviceName)
		_, err = c.GetSecret(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting expected Secret")

		// Should create a Role for the Secret
		_, err = c.GetRole(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting expected Secret Role")

		// Should create a RoleBinding for the Secret
		_, err = c.GetRoleBinding(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting exptected Secret RoleBinding")

		// Should create a system:auth-delegator Cluster RoleBinding
		_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", serviceName))
		require.NoError(GinkgoT(), err, "error getting expected system:auth-delegator ClusterRoleBinding")

		// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
		_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", serviceName))
		require.NoError(GinkgoT(), err, "error getting expected extension-apiserver-authentication-reader RoleBinding")

		// Store the ca sha annotation
		oldCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[olm.OLMCAHashAnnotationKey]
		require.True(GinkgoT(), ok, "expected olm sha annotation not present on existing pod template")

		// Induce a cert rotation
		now := metav1.Now()
		fetchedCSV.Status.CertsLastUpdated = &now
		fetchedCSV.Status.CertsRotateAt = &now
		fetchedCSV, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).UpdateStatus(context.TODO(), fetchedCSV, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, func(csv *v1alpha1.ClusterServiceVersion) bool {
			// Should create deployment
			dep, err = c.GetDeployment(testNamespace, depName)
			require.NoError(GinkgoT(), err)

			// Should have a new ca hash annotation
			newCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[olm.OLMCAHashAnnotationKey]
			require.True(GinkgoT(), ok, "expected olm sha annotation not present in new pod template")

			if newCAAnnotation != oldCAAnnotation {
				// Check for success
				return csvSucceededChecker(csv)
			}

			return false
		})
		require.NoError(GinkgoT(), err, "failed to rotate cert")

		// Get the APIService UID
		oldAPIServiceUID := apiService.GetUID()

		// Delete the APIService
		err = c.DeleteAPIService(apiServiceName, &metav1.DeleteOptions{})
		require.NoError(GinkgoT(), err)

		// Wait for CSV success
		fetchedCSV, err = fetchCSV(GinkgoT(), crc, csv.GetName(), testNamespace, func(csv *v1alpha1.ClusterServiceVersion) bool {
			// Should create an APIService
			apiService, err := c.GetAPIService(apiServiceName)
			if err != nil {
				require.True(GinkgoT(), k8serrors.IsNotFound(err))
				return false
			}

			if csvSucceededChecker(csv) {
				require.NotEqual(GinkgoT(), oldAPIServiceUID, apiService.GetUID())
				return true
			}

			return false
		})
		require.NoError(GinkgoT(), err)
	})
	It("update with owned API service", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSVs for the hat-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")

		// Create the APIService CSV
		_, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should create Deployment
		_, err = c.GetDeployment(testNamespace, depName)
		require.NoError(GinkgoT(), err, "error getting expected Deployment")

		// Should create APIService
		_, err = c.GetAPIService(apiServiceName)
		require.NoError(GinkgoT(), err, "error getting expected APIService")

		// Should create Service
		serviceName := fmt.Sprintf("%s-service", depName)
		_, err = c.GetService(testNamespace, serviceName)
		require.NoError(GinkgoT(), err, "error getting expected Service")

		// Should create certificate Secret
		secretName := fmt.Sprintf("%s-cert", serviceName)
		_, err = c.GetSecret(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting expected Secret")

		// Should create a Role for the Secret
		_, err = c.GetRole(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting expected Secret Role")

		// Should create a RoleBinding for the Secret
		_, err = c.GetRoleBinding(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting exptected Secret RoleBinding")

		// Should create a system:auth-delegator Cluster RoleBinding
		_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", serviceName))
		require.NoError(GinkgoT(), err, "error getting expected system:auth-delegator ClusterRoleBinding")

		// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
		_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", serviceName))
		require.NoError(GinkgoT(), err, "error getting expected extension-apiserver-authentication-reader RoleBinding")

		// Create a new CSV that owns the same API Service and replace the old CSV
		csv2 := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces:       csv.Name,
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv2.SetName("csv-hat-2")

		// Create CSV2 to replace CSV
		cleanupCSV2, err := createCSV(GinkgoT(), c, crc, csv2, testNamespace, false, true)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV2()

		_, err = fetchCSV(GinkgoT(), crc, csv2.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should create Deployment
		_, err = c.GetDeployment(testNamespace, depName)
		require.NoError(GinkgoT(), err, "error getting expected Deployment")

		// Should create APIService
		_, err = c.GetAPIService(apiServiceName)
		require.NoError(GinkgoT(), err, "error getting expected APIService")

		// Should create Service
		_, err = c.GetService(testNamespace, serviceName)
		require.NoError(GinkgoT(), err, "error getting expected Service")

		// Should create certificate Secret
		secretName = fmt.Sprintf("%s-cert", serviceName)
		_, err = c.GetSecret(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting expected Secret")

		// Should create a Role for the Secret
		_, err = c.GetRole(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting expected Secret Role")

		// Should create a RoleBinding for the Secret
		_, err = c.GetRoleBinding(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting exptected Secret RoleBinding")

		// Should create a system:auth-delegator Cluster RoleBinding
		_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", serviceName))
		require.NoError(GinkgoT(), err, "error getting expected system:auth-delegator ClusterRoleBinding")

		// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
		_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", serviceName))
		require.NoError(GinkgoT(), err, "error getting expected extension-apiserver-authentication-reader RoleBinding")

		// Should eventually GC the CSV
		err = waitForCSVToDelete(GinkgoT(), crc, csv.Name)
		require.NoError(GinkgoT(), err)

		// Rename the initial CSV
		csv.SetName("csv-hat-3")

		// Recreate the old CSV
		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, true)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		fetched, err := fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, buildCSVReasonChecker(v1alpha1.CSVReasonOwnerConflict))
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), string(v1alpha1.CSVPhaseFailed), string(fetched.Status.Phase))
	})
	It("create same CSV with owned API service multi namespace", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create new namespace in a new operator group
		secondNamespaceName := genName(testNamespace + "-")
		matchingLabel := map[string]string{"inGroup": secondNamespaceName}

		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   secondNamespaceName,
				Labels: matchingLabel,
			},
		}, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err = c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), secondNamespaceName, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		// Create a new operator group for the new namespace
		operatorGroup := v1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("e2e-operator-group-"),
				Namespace: secondNamespaceName,
			},
			Spec: v1.OperatorGroupSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: matchingLabel,
				},
			},
		}
		_, err = crc.OperatorsV1().OperatorGroups(secondNamespaceName).Create(context.TODO(), &operatorGroup, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err = crc.OperatorsV1().OperatorGroups(secondNamespaceName).Delete(context.TODO(), operatorGroup.Name, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		expectedOperatorGroupStatus := v1.OperatorGroupStatus{
			Namespaces: []string{secondNamespaceName},
		}
		GinkgoT().Log("Waiting on new operator group to have correct status")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, fetchErr := crc.OperatorsV1().OperatorGroups(secondNamespaceName).Get(context.TODO(), operatorGroup.Name, metav1.GetOptions{})
			if fetchErr != nil {
				return false, fetchErr
			}
			if len(fetched.Status.Namespaces) > 0 {
				require.ElementsMatch(GinkgoT(), expectedOperatorGroupStatus.Namespaces, fetched.Status.Namespaces)
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSVs for the hat-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")

		// Create the initial CSV
		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should create Deployment
		_, err = c.GetDeployment(testNamespace, depName)
		require.NoError(GinkgoT(), err, "error getting expected Deployment")

		// Should create APIService
		_, err = c.GetAPIService(apiServiceName)
		require.NoError(GinkgoT(), err, "error getting expected APIService")

		// Should create Service
		serviceName := fmt.Sprintf("%s-service", depName)
		_, err = c.GetService(testNamespace, serviceName)
		require.NoError(GinkgoT(), err, "error getting expected Service")

		// Should create certificate Secret
		secretName := fmt.Sprintf("%s-cert", serviceName)
		_, err = c.GetSecret(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting expected Secret")

		// Should create a Role for the Secret
		_, err = c.GetRole(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting expected Secret Role")

		// Should create a RoleBinding for the Secret
		_, err = c.GetRoleBinding(testNamespace, secretName)
		require.NoError(GinkgoT(), err, "error getting exptected Secret RoleBinding")

		// Should create a system:auth-delegator Cluster RoleBinding
		_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", serviceName))
		require.NoError(GinkgoT(), err, "error getting expected system:auth-delegator ClusterRoleBinding")

		// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
		_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", serviceName))
		require.NoError(GinkgoT(), err, "error getting expected extension-apiserver-authentication-reader RoleBinding")

		// Create a new CSV that owns the same API Service but in a different namespace
		csv2 := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv2.SetName("csv-hat-2")

		// Create CSV2 to replace CSV
		_, err = createCSV(GinkgoT(), c, crc, csv2, secondNamespaceName, false, true)
		require.NoError(GinkgoT(), err)

		_, err = fetchCSV(GinkgoT(), crc, csv2.Name, secondNamespaceName, csvFailedChecker)
		require.NoError(GinkgoT(), err)
	})
	It("orphaned API service clean up", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())

		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		apiService := &apiregistrationv1.APIService{
			ObjectMeta: metav1.ObjectMeta{
				Name: apiServiceName,
			},
			Spec: apiregistrationv1.APIServiceSpec{
				Group:                mockGroup,
				Version:              version,
				GroupPriorityMinimum: 100,
				VersionPriority:      100,
			},
		}

		watcher, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Watch(context.TODO(), metav1.ListOptions{FieldSelector: "metadata.name=" + apiServiceName})
		require.NoError(GinkgoT(), err)

		deleted := make(chan struct{})
		quit := make(chan struct{})
		defer close(quit)
		go func() {
			events := watcher.ResultChan()
			for {
				select {
				case <-quit:
					return
				case evt := <-events:
					if evt.Type == watch.Deleted {
						deleted <- struct{}{}
					}
				case <-time.After(pollDuration):
					require.FailNow(GinkgoT(), "orphaned apiservice not cleaned up as expected")
				}
			}
		}()

		_, err = c.CreateAPIService(apiService)
		require.NoError(GinkgoT(), err, "error creating expected APIService")
		orphanedAPISvc, err := c.GetAPIService(apiServiceName)
		require.NoError(GinkgoT(), err, "error getting expected APIService")

		newLabels := map[string]string{"olm.owner": "hat-serverfd4r5", "olm.owner.kind": "ClusterServiceVersion", "olm.owner.namespace": "nonexistent-namespace"}
		orphanedAPISvc.SetLabels(newLabels)
		_, err = c.UpdateAPIService(orphanedAPISvc)
		require.NoError(GinkgoT(), err, "error updating APIService")
		<-deleted

		_, err = c.CreateAPIService(apiService)
		require.NoError(GinkgoT(), err, "error creating expected APIService")
		orphanedAPISvc, err = c.GetAPIService(apiServiceName)
		require.NoError(GinkgoT(), err, "error getting expected APIService")

		newLabels = map[string]string{"olm.owner": "hat-serverfd4r5", "olm.owner.kind": "ClusterServiceVersion", "olm.owner.namespace": testNamespace}
		orphanedAPISvc.SetLabels(newLabels)
		_, err = c.UpdateAPIService(orphanedAPISvc)
		require.NoError(GinkgoT(), err, "error updating APIService")
		<-deleted
	})
	It("update same deployment name", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create dependency first (CRD)
		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})

		// Create "current" CSV
		nginxName := genName("nginx-")
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(nginxName),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		require.NoError(GinkgoT(), err)
		defer cleanupCRD()
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster",
						},
					},
				},
			},
		}

		// Don't need to cleanup this CSV, it will be deleted by the upgrade process
		_, err = createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)

		// Wait for current CSV to succeed
		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), dep)

		// Create "updated" CSV
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					// Same name
					Name: strategy.DeploymentSpecs[0].Name,
					// Different spec
					Spec: newNginxDeployment(nginxName),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		csvNew := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csv.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster",
						},
					},
				},
			},
		}

		cleanupNewCSV, err := createCSV(GinkgoT(), c, crc, csvNew, testNamespace, true, false)
		require.NoError(GinkgoT(), err)
		defer cleanupNewCSV()

		// Wait for updated CSV to succeed
		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csvNew.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should have updated existing deployment
		depUpdated, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), depUpdated)
		require.Equal(GinkgoT(), depUpdated.Spec.Template.Spec.Containers[0].Name, strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name)

		// Should eventually GC the CSV
		err = waitForCSVToDelete(GinkgoT(), crc, csv.Name)
		require.NoError(GinkgoT(), err)

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(GinkgoT(), crc, csvNew.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		compareResources(GinkgoT(), fetchedCSV, sameCSV)
	})
	It("update different deployment name", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create dependency first (CRD)
		crdPlural := genName("ins2")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		// create "current" CSV
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster2",
						},
					},
				},
			},
		}

		// don't need to clean up this CSV, it will be deleted by the upgrade process
		_, err = createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)

		// Wait for current CSV to succeed
		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), dep)

		// Create "updated" CSV
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep2"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		csvNew := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv2"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csv.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster2",
						},
					},
				},
			},
		}

		cleanupNewCSV, err := createCSV(GinkgoT(), c, crc, csvNew, testNamespace, true, false)
		require.NoError(GinkgoT(), err)
		defer cleanupNewCSV()

		// Wait for updated CSV to succeed
		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csvNew.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(GinkgoT(), crc, csvNew.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		compareResources(GinkgoT(), fetchedCSV, sameCSV)

		// Should have created new deployment and deleted old
		depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), depNew)
		err = waitForDeploymentToDelete(GinkgoT(), c, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)

		// Should eventually GC the CSV
		err = waitForCSVToDelete(GinkgoT(), crc, csv.Name)
		require.NoError(GinkgoT(), err)
	})
	It("update multiple intermediates", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create dependency first (CRD)
		crdPlural := genName("ins3")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		// create "current" CSV
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster3",
						},
					},
				},
			},
		}

		// don't need to clean up this CSV, it will be deleted by the upgrade process
		_, err = createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)

		// Wait for current CSV to succeed
		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), dep)

		// Create "updated" CSV
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep2"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		csvNew := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv2"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csv.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster3",
						},
					},
				},
			},
		}

		cleanupNewCSV, err := createCSV(GinkgoT(), c, crc, csvNew, testNamespace, true, false)
		require.NoError(GinkgoT(), err)
		defer cleanupNewCSV()

		// Wait for updated CSV to succeed
		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csvNew.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(GinkgoT(), crc, csvNew.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		compareResources(GinkgoT(), fetchedCSV, sameCSV)

		// Should have created new deployment and deleted old
		depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), depNew)
		err = waitForDeploymentToDelete(GinkgoT(), c, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)

		// Should eventually GC the CSV
		err = waitForCSVToDelete(GinkgoT(), crc, csv.Name)
		require.NoError(GinkgoT(), err)
	})
	It("update in place", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create dependency first (CRD)
		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})

		// Create "current" CSV
		nginxName := genName("nginx-")
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(nginxName),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		require.NoError(GinkgoT(), err)
		defer cleanupCRD()
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, true)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		// Wait for current CSV to succeed
		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), dep)

		// Create "updated" CSV
		strategyNew := strategy
		strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name:  genName("nginx-"),
				Image: *dummyImage,
				Ports: []corev1.ContainerPort{
					{
						ContainerPort: 80,
					},
				},
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
		}

		// Also set something outside the spec template - this should be ignored
		var five int32 = 5
		strategyNew.DeploymentSpecs[0].Spec.Replicas = &five

		require.NoError(GinkgoT(), err)

		fetchedCSV.Spec.InstallStrategy.StrategySpec = strategyNew

		// Update CSV directly
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Update(context.TODO(), fetchedCSV, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		// wait for deployment spec to be updated
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
			if err != nil {
				return false, err
			}
			fmt.Println("waiting for deployment to update...")
			return fetched.Spec.Template.Spec.Containers[0].Name == strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name, nil
		})
		require.NoError(GinkgoT(), err)

		// Wait for updated CSV to succeed
		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		depUpdated, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), depUpdated)

		// Deployment should have changed even though the CSV is otherwise the same
		require.Equal(GinkgoT(), depUpdated.Spec.Template.Spec.Containers[0].Name, strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name)
		require.Equal(GinkgoT(), depUpdated.Spec.Template.Spec.Containers[0].Image, strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Image)

		// Field updated even though template spec didn't change, because it was part of a template spec change as well
		require.Equal(GinkgoT(), *depUpdated.Spec.Replicas, *strategyNew.DeploymentSpecs[0].Spec.Replicas)
	})
	It("update multiple version CRD", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create initial CRD which has 2 versions: v1alpha1 & v1alpha2
		crdPlural := genName("ins4")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
					},
					{
						Name:    "v1alpha2",
						Served:  true,
						Storage: false,
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		// create initial deployment strategy
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep1-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		// First CSV with owning CRD v1alpha1
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster4",
						},
					},
				},
			},
		}

		// CSV will be deleted by the upgrade process later
		_, err = createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)

		// Wait for current CSV to succeed
		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should have created deployment
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), dep)

		// Create updated deployment strategy
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep2-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		// Second CSV with owning CRD v1alpha1 and v1alpha2
		csvNew := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv2"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csv.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster4",
						},
						{
							Name:        crdName,
							Version:     "v1alpha2",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster4",
						},
					},
				},
			},
		}

		// Create newly updated CSV
		_, err = createCSV(GinkgoT(), c, crc, csvNew, testNamespace, false, false)
		require.NoError(GinkgoT(), err)

		// Wait for updated CSV to succeed
		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csvNew.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err := fetchCSV(GinkgoT(), crc, csvNew.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		compareResources(GinkgoT(), fetchedCSV, sameCSV)

		// Should have created new deployment and deleted old one
		depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), depNew)
		err = waitForDeploymentToDelete(GinkgoT(), c, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)

		// Create updated deployment strategy
		strategyNew2 := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep3-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}
		require.NoError(GinkgoT(), err)

		// Third CSV with owning CRD v1alpha2
		csvNew2 := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv3"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: csvNew.Name,
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategyNew2,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha2",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster4",
						},
					},
				},
			},
		}

		// Create newly updated CSV
		cleanupNewCSV, err := createCSV(GinkgoT(), c, crc, csvNew2, testNamespace, true, false)
		require.NoError(GinkgoT(), err)
		defer cleanupNewCSV()

		// Wait for updated CSV to succeed
		fetchedCSV, err = fetchCSV(GinkgoT(), crc, csvNew2.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Fetch cluster service version again to check for unnecessary control loops
		sameCSV, err = fetchCSV(GinkgoT(), crc, csvNew2.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		compareResources(GinkgoT(), fetchedCSV, sameCSV)

		// Should have created new deployment and deleted old one
		depNew, err = c.GetDeployment(testNamespace, strategyNew2.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), depNew)
		err = waitForDeploymentToDelete(GinkgoT(), c, strategyNew.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)

		// Should clean up the CSV
		err = waitForCSVToDelete(GinkgoT(), crc, csvNew.Name)
		require.NoError(GinkgoT(), err)
	})
	It("update modify deployment name", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create dependency first (CRD)
		crdPlural := genName("ins2")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		// create "current" CSV
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
				{
					Name: "dep2-test",
					Spec: newNginxDeployment("nginx2"),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster2",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, true, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		// Wait for current CSV to succeed
		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should have created deployments
		dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), dep)
		dep2, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[1].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), dep2)

		// Create "updated" CSV
		strategyNew := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep3-"),
					Spec: newNginxDeployment(genName("nginx3-")),
				},
				{
					Name: "dep2-test",
					Spec: newNginxDeployment("nginx2"),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		// Fetch the current csv
		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Update csv with same strategy with different deployment's name
		fetchedCSV.Spec.InstallStrategy.StrategySpec = strategyNew

		// Update the current csv with the new csv
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Update(context.TODO(), fetchedCSV, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		// Wait for new deployment to exist
		err = waitForDeployment(c, strategyNew.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)

		// Wait for updated CSV to succeed
		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should have created new deployment and deleted old
		depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), depNew)
		// Make sure the unchanged deployment still exists
		depNew2, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[1].Name)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), depNew2)
		err = waitForDeploymentToDelete(GinkgoT(), c, strategy.DeploymentSpecs[0].Name)
		require.NoError(GinkgoT(), err)
	})
	It("create requirements events", func() {
		GinkgoT().Skip()
		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		sa := corev1.ServiceAccount{}
		sa.SetName(genName("sa-"))
		sa.SetNamespace(testNamespace)
		_, err := c.CreateServiceAccount(&sa)
		require.NoError(GinkgoT(), err, "could not create ServiceAccount")

		permissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: sa.GetName(),
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"create"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
					{
						Verbs:     []string{"delete"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: sa.GetName(),
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"deployment"},
					},
				},
			},
		}

		depName := genName("dep-")
		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
				// Cheating a little; this is an APIservice that will exist for the e2e tests
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Required: []v1alpha1.APIServiceDescription{
						{
							Group:       "packages.operators.coreos.com",
							Version:     "v1",
							Kind:        "PackageManifest",
							DisplayName: "Package Manifest",
							Description: "An apiservice that exists",
						},
					},
				},
			},
		}

		// Create Role/Cluster Roles and RoleBindings
		role := rbacv1.Role{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
				{
					Verbs:     []string{"delete"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		role.SetName("test-role")
		role.SetNamespace(testNamespace)
		_, err = c.CreateRole(&role)
		require.NoError(GinkgoT(), err, "could not create Role")

		roleBinding := rbacv1.RoleBinding{
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
				Name:     role.GetName(),
			},
		}
		roleBinding.SetName(genName("dep-"))
		roleBinding.SetNamespace(testNamespace)
		_, err = c.CreateRoleBinding(&roleBinding)
		require.NoError(GinkgoT(), err, "could not create RoleBinding")

		clusterRole := rbacv1.ClusterRole{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		clusterRole.SetName(genName("dep-"))
		_, err = c.CreateClusterRole(&clusterRole)
		require.NoError(GinkgoT(), err, "could not create ClusterRole")

		clusterRoleBinding := rbacv1.ClusterRoleBinding{
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
				Name:     clusterRole.GetName(),
			},
		}
		clusterRoleBinding.SetName(genName("dep-"))
		_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
		require.NoError(GinkgoT(), err, "could not create ClusterRoleBinding")

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		listOptions := metav1.ListOptions{
			FieldSelector: "involvedObject.kind=ClusterServiceVersion",
		}

		// Get events from test namespace for CSV
		eventsList, err := c.KubernetesInterface().CoreV1().Events(testNamespace).List(context.TODO(), listOptions)
		require.NoError(GinkgoT(), err)
		latestEvent := findLastEvent(eventsList)
		require.Equal(GinkgoT(), string(latestEvent.Reason), "InstallSucceeded")

		// Edit role
		updatedRole := rbacv1.Role{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		}
		updatedRole.SetName("test-role")
		updatedRole.SetNamespace(testNamespace)
		_, err = c.UpdateRole(&updatedRole)
		require.NoError(GinkgoT(), err)

		// Check CSV status
		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvPendingChecker)
		require.NoError(GinkgoT(), err)

		// Check event
		eventsList, err = c.KubernetesInterface().CoreV1().Events(testNamespace).List(context.TODO(), listOptions)
		require.NoError(GinkgoT(), err)
		latestEvent = findLastEvent(eventsList)
		require.Equal(GinkgoT(), string(latestEvent.Reason), "RequirementsNotMet")

		// Reverse the updated role
		_, err = c.UpdateRole(&role)
		require.NoError(GinkgoT(), err)

		// Check CSV status
		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Check event
		eventsList, err = c.KubernetesInterface().CoreV1().Events(testNamespace).List(context.TODO(), listOptions)
		require.NoError(GinkgoT(), err)
		latestEvent = findLastEvent(eventsList)
		require.Equal(GinkgoT(), string(latestEvent.Reason), "InstallSucceeded")
	})
	// TODO: test behavior when replaces field doesn't point to existing CSV
	It("status invalid CSV", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create CRD
		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		})
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		// create CSV
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: genName("dep-"),
					Spec: newNginxDeployment(genName("nginx-")),
				},
			},
		}

		require.NoError(GinkgoT(), err)

		csv := v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("csv"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        crdName,
							Version:     "apiextensions.k8s.io/v1alpha1", // purposely invalid, should be just v1alpha1 to match CRD
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: "In the cluster2",
						},
					},
				},
			},
		}

		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, true, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		notServedStatus := v1alpha1.RequirementStatus{
			Group:   "apiextensions.k8s.io",
			Version: "v1beta1",
			Kind:    "CustomResourceDefinition",
			Name:    crdName,
			Status:  v1alpha1.RequirementStatusReasonNotPresent,
			Message: "CRD version not served",
		}
		csvCheckPhaseAndRequirementStatus := func(csv *v1alpha1.ClusterServiceVersion) bool {
			if csv.Status.Phase == v1alpha1.CSVPhasePending {
				for _, status := range csv.Status.RequirementStatus {
					if status.Message == notServedStatus.Message {
						return true
					}
				}
			}
			return false
		}

		fetchedCSV, err := fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvCheckPhaseAndRequirementStatus)
		require.NoError(GinkgoT(), err)

		require.Contains(GinkgoT(), fetchedCSV.Status.RequirementStatus, notServedStatus)
	})

	It("api service resource migrated if adoptable", func() {
		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSVs for the hat-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")
		csv.SetNamespace(testNamespace)

		createLegacyAPIResources(&csv, owned[0])

		// Create the APIService CSV
		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		checkLegacyAPIResources(owned[0], true)
	})

	It("API service resource not migrated if not adoptable", func() {
		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		depName := genName("hat-server")
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fedora"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
		apiServiceName := strings.Join([]string{version, mockGroup}, ".")

		// Create CSVs for the hat-server
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
		for i, kind := range mockKinds {
			owned[i] = v1alpha1.APIServiceDescription{
				Name:           apiServiceName,
				Group:          mockGroup,
				Version:        version,
				Kind:           kind,
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    kind,
				Description:    fmt.Sprintf("A %s", kind),
			}
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")
		csv.SetNamespace(testNamespace)

		createLegacyAPIResources( nil, owned[0])

		// Create the APIService CSV
		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		checkLegacyAPIResources(owned[0], false)

		// Cleanup the resources created for this test that were not cleaned up.
		deleteLegacyAPIResources(owned[0])
	})

	It("multiple API services on a single pod", func() {
		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create the deployment that both APIServices will be deployed to.
		depName := genName("hat-server")

		// Define the expected mock APIService settings.
		version := "v1alpha1"
		apiService1Group := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		apiService1GroupVersion := strings.Join([]string{apiService1Group, version}, "/")
		apiService1Kinds := []string{"fedora"}
		apiService1Name := strings.Join([]string{version, apiService1Group}, ".")

		apiService2Group := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		apiService2GroupVersion := strings.Join([]string{apiService2Group, version}, "/")
		apiService2Kinds := []string{"fez"}
		apiService2Name := strings.Join([]string{version, apiService2Group}, ".")

		// Create the deployment spec with the two APIServices.
		mockGroupVersionKinds := []mockGroupVersionKind{
			{
				depName,
				apiService1GroupVersion,
				apiService1Kinds,
				5443,
			},
			{
				depName,
				apiService2GroupVersion,
				apiService2Kinds,
				5444,
			},
		}
		depSpec := newMockExtServerDeployment(depName, mockGroupVersionKinds)

		// Create the CSV.
		strategy := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
				{
					Name: depName,
					Spec: depSpec,
				},
			},
		}

		// Update the owned APIServices two include the two APIServices.
		owned := []v1alpha1.APIServiceDescription{
			{
				Name:           apiService1Name,
				Group:          apiService1Group,
				Version:        version,
				Kind:           apiService1Kinds[0],
				DeploymentName: depName,
				ContainerPort:  int32(5443),
				DisplayName:    apiService1Kinds[0],
				Description:    fmt.Sprintf("A %s", apiService1Kinds[0]),
			},
			{
				Name:           apiService2Name,
				Group:          apiService2Group,
				Version:        version,
				Kind:           apiService2Kinds[0],
				DeploymentName: depName,
				ContainerPort:  int32(5444),
				DisplayName:    apiService2Kinds[0],
				Description:    fmt.Sprintf("A %s", apiService2Kinds[0]),
			},
		}

		csv := v1alpha1.ClusterServiceVersion{
			Spec: v1alpha1.ClusterServiceVersionSpec{
				MinKubeVersion: "0.0.0",
				InstallModes: []v1alpha1.InstallMode{
					{
						Type:      v1alpha1.InstallModeTypeOwnNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeSingleNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeMultiNamespace,
						Supported: true,
					},
					{
						Type:      v1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: strategy,
				},
				APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
					Owned: owned,
				},
			},
		}
		csv.SetName("csv-hat-1")
		csv.SetNamespace(testNamespace)

		// Create the APIService CSV
		cleanupCSV, err := createCSV(GinkgoT(), c, crc, csv, testNamespace, false, false)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, csv.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Check that the APIService caBundles are equal
		apiService1, err := c.GetAPIService(apiService1Name)
		require.NoError(GinkgoT(), err)

		apiService2, err := c.GetAPIService(apiService2Name)
		require.NoError(GinkgoT(), err)

		require.Equal(GinkgoT(), apiService1.Spec.CABundle, apiService2.Spec.CABundle)
	})
})

var singleInstance = int32(1)

type cleanupFunc func()

var immediateDeleteGracePeriod int64 = 0

func findLastEvent(events *corev1.EventList) (event corev1.Event) {
	var latestTime metav1.Time
	var latestInd int
	for i, item := range events.Items {
		if i != 0 {
			if latestTime.Before(&item.LastTimestamp) {
				latestTime = item.LastTimestamp
				latestInd = i
			}
		} else {
			latestTime = item.LastTimestamp
		}
	}
	return events.Items[latestInd]
}

func buildCSVCleanupFunc(t GinkgoTInterface, c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, deleteCRDs, deleteAPIServices bool) cleanupFunc {
	return func() {
		require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(context.TODO(), csv.GetName(), metav1.DeleteOptions{}))
		if deleteCRDs {
			for _, crd := range csv.Spec.CustomResourceDefinitions.Owned {
				buildCRDCleanupFunc(c, crd.Name)()
			}
		}

		if deleteAPIServices {
			for _, desc := range csv.GetOwnedAPIServiceDescriptions() {
				buildAPIServiceCleanupFunc(c, desc.Name)()
			}
		}

		require.NoError(t, waitForDelete(func() error {
			_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), csv.GetName(), metav1.GetOptions{})
			return err
		}))
	}
}

func createCSV(t GinkgoTInterface, c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, cleanupCRDs, cleanupAPIServices bool) (cleanupFunc, error) {
	csv.Kind = v1alpha1.ClusterServiceVersionKind
	csv.APIVersion = v1alpha1.SchemeGroupVersion.String()
	_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Create(context.TODO(), &csv, metav1.CreateOptions{})
	require.NoError(t, err)
	return buildCSVCleanupFunc(t, c, crc, csv, namespace, cleanupCRDs, cleanupAPIServices), nil

}

func buildCRDCleanupFunc(c operatorclient.ClientInterface, crdName string) cleanupFunc {
	return func() {
		err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Delete(context.TODO(), crdName, metav1.DeleteOptions{GracePeriodSeconds: &immediateDeleteGracePeriod})
		if err != nil {
			fmt.Println(err)
		}

		err = waitForDelete(func() error {
			_, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
			return err
		})
		if err != nil {
			fmt.Println(err)
		}
	}
}

func buildAPIServiceCleanupFunc(c operatorclient.ClientInterface, apiServiceName string) cleanupFunc {
	return func() {
		err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Delete(context.TODO(), apiServiceName, metav1.DeleteOptions{GracePeriodSeconds: &immediateDeleteGracePeriod})
		if err != nil {
			fmt.Println(err)
		}

		err = waitForDelete(func() error {
			_, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Get(context.TODO(), apiServiceName, metav1.GetOptions{})
			return err
		})
		if err != nil {
			fmt.Println(err)
		}
	}
}

func createCRD(c operatorclient.ClientInterface, crd apiextensions.CustomResourceDefinition) (cleanupFunc, error) {
	out := &v1beta1.CustomResourceDefinition{}
	scheme := runtime.NewScheme()
	if err := apiextensions.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := v1beta1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := scheme.Convert(&crd, out, nil); err != nil {
		return nil, err
	}
	_, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Create(context.TODO(), out, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return buildCRDCleanupFunc(c, crd.GetName()), nil
}

func newNginxDeployment(name string) appsv1.DeploymentSpec {
	return appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": name,
			},
		},
		Replicas: &singleInstance,
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": name,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  genName("nginx"),
						Image: *dummyImage,
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 80,
							},
						},
						ImagePullPolicy: corev1.PullIfNotPresent,
					},
				},
			},
		},
	}
}

type mockGroupVersionKind struct {
	Name             string
	MockGroupVersion string
	MockKinds        []string
	Port             int
}

func newMockExtServerDeployment(labelName string, mGVKs []mockGroupVersionKind) appsv1.DeploymentSpec {

	// Create the list of containers
	containers := []corev1.Container{}
	for _, mGVK := range mGVKs {
		containers = append(containers, corev1.Container{
			Name:    genName(mGVK.Name),
			Image:   "quay.io/coreos/mock-extension-apiserver:master",
			Command: []string{"/bin/mock-extension-apiserver"},
			Args: []string{
				"-v=4",
				"--mock-kinds",
				strings.Join(mGVK.MockKinds, ","),
				"--mock-group-version",
				mGVK.MockGroupVersion,
				"--secure-port",
				strconv.Itoa(mGVK.Port),
				"--debug",
			},
			Ports: []corev1.ContainerPort{
				{
					ContainerPort: int32(mGVK.Port),
				},
			},
			ImagePullPolicy: corev1.PullIfNotPresent,
		})
	}
	return appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": labelName,
			},
		},
		Replicas: &singleInstance,
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": labelName,
				},
			},
			Spec: corev1.PodSpec{
				Containers: containers,
			},
		},
	}
}

type csvConditionChecker func(csv *v1alpha1.ClusterServiceVersion) bool

func buildCSVConditionChecker(phases ...v1alpha1.ClusterServiceVersionPhase) csvConditionChecker {
	return func(csv *v1alpha1.ClusterServiceVersion) bool {
		conditionMet := false
		for _, phase := range phases {
			conditionMet = conditionMet || csv.Status.Phase == phase
		}
		return conditionMet
	}
}

func buildCSVReasonChecker(reasons ...v1alpha1.ConditionReason) csvConditionChecker {
	return func(csv *v1alpha1.ClusterServiceVersion) bool {
		conditionMet := false
		for _, reason := range reasons {
			conditionMet = conditionMet || csv.Status.Reason == reason
		}
		return conditionMet
	}
}

var csvPendingChecker = buildCSVConditionChecker(v1alpha1.CSVPhasePending)
var csvSucceededChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded)
var csvReplacingChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseReplacing, v1alpha1.CSVPhaseDeleting)
var csvFailedChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseFailed)
var csvAnyChecker = buildCSVConditionChecker(v1alpha1.CSVPhasePending, v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVPhaseReplacing, v1alpha1.CSVPhaseDeleting, v1alpha1.CSVPhaseFailed)
var csvCopiedChecker = buildCSVReasonChecker(v1alpha1.CSVReasonCopied)

func fetchCSV(t GinkgoTInterface, c versioned.Interface, name, namespace string, checker csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	var fetched *v1alpha1.ClusterServiceVersion
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err = c.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		t.Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return checker(fetched), nil
	})

	if err != nil {
		t.Logf("never got correct status: %#v", fetched.Status)
	}
	return fetched, err
}

func awaitCSV(t GinkgoTInterface, c versioned.Interface, namespace, name string, checker csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	var fetched *v1alpha1.ClusterServiceVersion
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err = c.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		t.Logf("%s - %s (%s): %s", name, fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return checker(fetched), nil
	})

	if err != nil {
		t.Logf("never got correct status: %#v", fetched.Status)
	}
	return fetched, err
}

func waitForDeployment(c operatorclient.ClientInterface, name string) error {
	return wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := c.GetDeployment(testNamespace, name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
}

func waitForDeploymentToDelete(t GinkgoTInterface, c operatorclient.ClientInterface, name string) error {
	return wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		t.Logf("waiting for deployment %s to delete", name)
		_, err := c.GetDeployment(testNamespace, name)
		if errors.IsNotFound(err) {
			t.Logf("deleted %s", name)
			return true, nil
		}
		if err != nil {
			t.Logf("err trying to delete %s: %s", name, err)
			return false, err
		}
		return false, nil
	})
}

func waitForCSVToDelete(t GinkgoTInterface, c versioned.Interface, name string) error {
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err := c.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(context.TODO(), name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		t.Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		if err != nil {
			return false, err
		}
		return false, nil
	})

	return err
}

func deleteLegacyAPIResources(desc v1alpha1.APIServiceDescription) {
	c := newKubeClient(GinkgoT())

	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)

	err := c.DeleteService(testNamespace, strings.Replace(apiServiceName, ".", "-", -1), &metav1.DeleteOptions{})
	require.NoError(GinkgoT(), err)

	err = c.DeleteSecret(testNamespace, apiServiceName+"-cert", &metav1.DeleteOptions{})
	require.NoError(GinkgoT(), err)

	err = c.DeleteRole(testNamespace, apiServiceName+"-cert", &metav1.DeleteOptions{})
	require.NoError(GinkgoT(), err)

	err = c.DeleteRoleBinding(testNamespace, apiServiceName+"-cert", &metav1.DeleteOptions{})
	require.NoError(GinkgoT(), err)

	err = c.DeleteClusterRoleBinding(apiServiceName+"-system:auth-delegator", &metav1.DeleteOptions{})
	require.NoError(GinkgoT(), err)

	err = c.DeleteRoleBinding("kube-system", apiServiceName+"-auth-reader", &metav1.DeleteOptions{})
	require.NoError(GinkgoT(), err)
}

func createLegacyAPIResources(csv *v1alpha1.ClusterServiceVersion, desc v1alpha1.APIServiceDescription) {
	c := newKubeClient(GinkgoT())

	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)

	// Attempt to create the legacy service
	service := corev1.Service{}
	service.SetName(strings.Replace(apiServiceName, ".", "-", -1))
	service.SetNamespace(testNamespace)
	if csv != nil {
		err := ownerutil.AddOwnerLabels(&service, csv)
		require.NoError(GinkgoT(), err)
	}

	service.Spec.Ports = []corev1.ServicePort{{Port: 433, TargetPort: intstr.FromInt(443)}}
	_, err := c.CreateService(&service)
	require.NoError(GinkgoT(), err)

	// Attempt to create the legacy secret
	secret := corev1.Secret{}
	secret.SetName(apiServiceName + "-cert")
	secret.SetNamespace(testNamespace)
	if csv != nil {
		err = ownerutil.AddOwnerLabels(&secret, csv)
		require.NoError(GinkgoT(), err)
	}

	_, err = c.CreateSecret(&secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		require.NoError(GinkgoT(), err)
	}

	// Attempt to create the legacy secret role
	role := rbacv1.Role{}
	role.SetName(apiServiceName + "-cert")
	role.SetNamespace(testNamespace)
	if csv != nil {
		err = ownerutil.AddOwnerLabels(&role, csv)
		require.NoError(GinkgoT(), err)
	}
	_, err = c.CreateRole(&role)
	require.NoError(GinkgoT(), err)

	// Attempt to create the legacy secret role binding
	roleBinding := rbacv1.RoleBinding{}
	roleBinding.SetName(apiServiceName + "-cert")
	roleBinding.SetNamespace(testNamespace)
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.GetName(),
	}
	if csv != nil {
		err = ownerutil.AddOwnerLabels(&roleBinding, csv)
		require.NoError(GinkgoT(), err)
	}

	_, err = c.CreateRoleBinding(&roleBinding)
	require.NoError(GinkgoT(), err)

	// Attempt to create the legacy authDelegatorClusterRoleBinding
	clusterRoleBinding := rbacv1.ClusterRoleBinding{}
	clusterRoleBinding.SetName(apiServiceName + "-system:auth-delegator")
	clusterRoleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     "system:auth-delegator",
	}
	if csv != nil {
		err = ownerutil.AddOwnerLabels(&clusterRoleBinding, csv)
		require.NoError(GinkgoT(), err)
	}
	_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
	require.NoError(GinkgoT(), err)

	// Attempt to create the legacy authReadingRoleBinding
	roleBinding.SetName(apiServiceName + "-auth-reader")
	roleBinding.SetNamespace("kube-system")
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     "extension-apiserver-authentication-reader",
	}
	_, err = c.CreateRoleBinding(&roleBinding)
	require.NoError(GinkgoT(), err)
}

func checkLegacyAPIResources(desc v1alpha1.APIServiceDescription, expectedIsNotFound bool) {
	c := newKubeClient(GinkgoT())
	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)

	// Attempt to create the legacy service
	_, err := c.GetService(testNamespace, strings.Replace(apiServiceName, ".", "-", -1))
	require.Equal(GinkgoT(), expectedIsNotFound, errors.IsNotFound(err))

	// Attempt to create the legacy secret
	_, err = c.GetSecret(testNamespace, apiServiceName+"-cert")
	require.Equal(GinkgoT(), expectedIsNotFound, errors.IsNotFound(err))

	// Attempt to create the legacy secret role
	_, err = c.GetRole(testNamespace, apiServiceName+"-cert")
	require.Equal(GinkgoT(), expectedIsNotFound, errors.IsNotFound(err))

	// Attempt to create the legacy secret role binding
	_, err = c.GetRoleBinding(testNamespace, apiServiceName+"-cert")
	require.Equal(GinkgoT(), expectedIsNotFound, errors.IsNotFound(err))

	// Attempt to create the legacy authDelegatorClusterRoleBinding
	_, err = c.GetClusterRoleBinding(apiServiceName + "-system:auth-delegator")
	require.Equal(GinkgoT(), expectedIsNotFound, errors.IsNotFound(err))

	// Attempt to create the legacy authReadingRoleBinding
	_, err = c.GetRoleBinding("kube-system", apiServiceName+"-auth-reader")
	require.Equal(GinkgoT(), expectedIsNotFound, errors.IsNotFound(err))
}
