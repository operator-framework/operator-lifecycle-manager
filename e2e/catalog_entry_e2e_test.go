package e2e

import (
	"strings"
	"testing"

	appsv1beta2 "k8s.io/api/apps/v1beta2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/coreos-inc/alm/pkg/apis"
	clusterserviceversionv1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	installplanv1alpha1 "github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	"github.com/coreos-inc/alm/pkg/registry"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// This file contains tests that are for specific OCS services. They're a sanity check that we can always do the right
// thing with catalog resources we need in tectonic. As such, they should be expected to be brittle compared to other
// e2e tests. They should eventually be removed, as they test functionality beyond the scope of ALM.

// As an infra owner, when I create an installplan for vault:
// * I should see a resolved installplan listing EtcdCluster, VaultService, vault ClusterServiceVersion, and etcd ClusterServiceVersion
// * I should see the resolved resources be created in the same namespace.
// * I should see a vault-operator deployment and an etcd-operator deployment in the same namespace
// * I should see service accounts for vault and etcd with permissions matching what’s listed in the ClusterServiceVersions
// * When I create a VaultService CR
//   * I should see a related EtcdCluster CR appear
//   * I should see pods for vault and etcd appear
func TestCreateInstallVaultPlanAndVerifyResources(t *testing.T) {
	c := newKubeClient(t)

	inMem, err := registry.NewInMemoryFromConfigMap(c, testNamespace, ocsConfigMap)
	require.NoError(t, err)
	require.NotNil(t, inMem)
	latestVaultCSV, err := inMem.FindCSVForPackageNameUnderChannel("vault", "alpha")
	require.NoError(t, err)
	require.NotNil(t, latestVaultCSV)

	vaultInstallPlan := installplanv1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       installplanv1alpha1.InstallPlanKind,
			APIVersion: installplanv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-verify-" + latestVaultCSV.Name,
			Namespace: testNamespace,
		},
		Spec: installplanv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{latestVaultCSV.Name},
			Approval:                   installplanv1alpha1.ApprovalAutomatic,
		},
	}

	// Create a new installplan for vault
	vaultUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&vaultInstallPlan)
	require.NoError(t, err)

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: vaultUnst})
	require.NoError(t, err)

	// Get InstallPlan and verify status
	fetchedInstallPlan, err := fetchInstallPlan(t, c, vaultInstallPlan.GetName(), installPlanCompleteChecker)

	require.Equal(t, installplanv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Ensure CustomResourceDefinitions and ClusterServiceVersion-v1s are present in the resolved InstallPlan
	requiredCSVs := []string{"etcdoperator", "vault-operator"}
	requiredCRDs := []string{"etcdclusters.etcd.database.coreos.com", "vaultservices.vault.security.coreos.com"}

	csvNames := map[string]string{}
	crdNames := []string{}

	for _, step := range fetchedInstallPlan.Status.Plan {
		if step.Resource.Kind == "CustomResourceDefinition" {
			crdNames = append(crdNames, step.Resource.Name)
		} else if step.Resource.Kind == clusterserviceversionv1.ClusterServiceVersionKind {
			csvNames[strings.Split(step.Resource.Name, ".")[0]] = step.Resource.Name
		}
	}

	// Check that the CSV and CRDs are actually present in the cluster as well
	for _, name := range requiredCSVs {
		require.NotEmpty(t, csvNames[name])

		t.Logf("Ensuring CSV %s is present in %s namespace", name, testNamespace)
		_, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, clusterserviceversionv1.ClusterServiceVersionKind, csvNames[name])
		require.NoError(t, err)
	}

	for _, name := range requiredCRDs {
		require.Contains(t, crdNames, name)

		t.Logf("Ensuring CRD %s is present in cluster", name)
		_, err := c.GetCustomResourceDefinition(name)
		require.NoError(t, err)
	}

	// * I should see service accounts for vault and etcd with permissions matching what’s listed in the ClusterServiceVersions
	for _, accountName := range []string{"etcd-operator", "vault-operator"} {
		var sa *corev1.ServiceAccount
		t.Logf("Looking for ServiceAccount %s in %s\n", accountName, testNamespace)

		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			sa, err = c.GetServiceAccount(testNamespace, accountName)
			if err != nil {
				if sErr := err.(*errors.StatusError); sErr.Status().Reason == metav1.StatusReasonNotFound {
					return false, nil
				}
				return false, err
			}

			return true, nil
		})

		require.NoError(t, err)
		require.Equal(t, accountName, sa.Name)

	}

	for _, deploymentName := range []string{"etcd-operator", "vault-operator"} {
		var deployment *appsv1beta2.Deployment
		t.Logf("Looking for Deployment %s in %s\n", deploymentName, testNamespace)

		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			deployment, err = c.GetDeployment(testNamespace, deploymentName)
			if err != nil {
				if sErr := err.(*errors.StatusError); sErr.Status().Reason == metav1.StatusReasonNotFound {
					return false, nil
				}
				return false, err
			}

			return true, nil
		})

		require.NoError(t, err)
		require.Equal(t, deploymentName, deployment.Name)
	}

	// * When I create a VaultService CR
	//   * I should see a related EtcdCluster CR appear
	//   * I should see pods for vault and etcd appear

	// Importing the vault-operator v1alpha1 api package causes all kinds of weird dependency conflicts
	// that I was unable to resolve.
	vaultService := map[string]interface{}{
		"kind":       "VaultService",
		"apiVersion": "vault.security.coreos.com/v1alpha1",
		"metadata": map[string]interface{}{
			"name":      "test-vault",
			"namespace": testNamespace,
		},
		"spec": map[string]interface{}{
			"nodes":   2,
			"version": vaultVersion,
		},
	}

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: vaultService})
	require.NoError(t, err)

	require.NoError(t, pollForCustomResource(t, c, "vault.security.coreos.com", "v1alpha1", "VaultService", "test-vault"))
	require.NoError(t, pollForCustomResource(t, c, "etcd.database.coreos.com", "v1beta2", "EtcdCluster", "test-vault-etcd"))

	etcdPods, err := awaitPods(t, c, "etcd_cluster=test-vault-etcd", expectedEtcdNodes)
	require.NoError(t, err)
	require.Equal(t, expectedEtcdNodes, len(etcdPods.Items))

	vaultPods, err := awaitPods(t, c, "vault_cluster=test-vault", vaultClusterSize)
	require.NoError(t, err)
	require.Equal(t, vaultClusterSize, len(vaultPods.Items))

}
