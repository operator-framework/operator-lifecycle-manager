package e2e

import (
	"strings"
	"testing"

	appsv1beta2 "k8s.io/api/apps/v1beta2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// This file contains tests that are for specific OCS services. They're a sanity check that we can always do the right
// thing with catalog resources we need in Tectonic. As such, they should be expected to be brittle compared to other
// e2e tests. They should eventually be removed, as they test functionality beyond the scope of ALM.

func fetchCatalogSource(t *testing.T, c operatorclient.ClientInterface, name string) (*v1alpha1.CatalogSource, error) {
	var fetchedCatalogSource *v1alpha1.CatalogSource
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSUnst, err := c.GetCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, testNamespace, v1alpha1.CatalogSourceKind, name)
		if err != nil {
			return false, err
		}

		err = runtime.DefaultUnstructuredConverter.FromUnstructured(fetchedCSUnst.Object, &fetchedCatalogSource)
		require.NoError(t, err)

		return true, nil
	})

	return fetchedCatalogSource, err
}

func TestInstallEtcdOCS(t *testing.T) {
	c := newKubeClient(t)

	catalogSource, err := fetchCatalogSource(t, c, ocsConfigMap)
	require.NoError(t, err)
	require.NotNil(t, catalogSource)
	inMem, err := registry.NewInMemoryFromConfigMap(c, testNamespace, catalogSource.Spec.ConfigMap)
	require.NoError(t, err)
	require.NotNil(t, inMem)
	latestEtcdCSV, err := inMem.FindCSVForPackageNameUnderChannel("etcd", "alpha")
	require.NoError(t, err)
	require.NotNil(t, latestEtcdCSV)

	etcdInstallPlan := v1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.InstallPlanKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-verify-" + latestEtcdCSV.Name,
			Namespace: testNamespace,
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{latestEtcdCSV.Name},
			Approval:                   v1alpha1.ApprovalAutomatic,
		},
	}

	// Create a new installplan for etcd
	etcdUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&etcdInstallPlan)
	require.NoError(t, err)

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: etcdUnst})
	require.NoError(t, err)

	// Get InstallPlan and verify status
	fetchedInstallPlan, err := fetchInstallPlan(t, c, etcdInstallPlan.GetName(), installPlanCompleteChecker)

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Ensure CustomResourceDefinitions and ClusterServiceVersion-v1s are present in the resolved InstallPlan
	requiredCSVs := []string{"etcdoperator"}
	requiredCRDs := []string{"etcdclusters.etcd.database.coreos.com", "etcdbackups.etcd.database.coreos.com", "etcdrestores.etcd.database.coreos.com"}

	csvNames := map[string]string{}
	crdNames := []string{}

	for _, step := range fetchedInstallPlan.Status.Plan {
		if step.Resource.Kind == "CustomResourceDefinition" {
			crdNames = append(crdNames, step.Resource.Name)
		} else if step.Resource.Kind == v1alpha1.ClusterServiceVersionKind {
			csvNames[strings.Split(step.Resource.Name, ".")[0]] = step.Resource.Name
		}
	}

	// Check that the CSV and CRDs are actually present in the cluster as well
	for _, name := range requiredCSVs {
		require.NotEmpty(t, csvNames[name])

		t.Logf("Ensuring CSV %s is present in %s namespace", name, testNamespace)
		_, err := c.GetCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, testNamespace, v1alpha1.ClusterServiceVersionKind, csvNames[name])
		require.NoError(t, err)
	}

	for _, name := range requiredCRDs {
		require.Contains(t, crdNames, name)

		t.Logf("Ensuring CRD %s is present in cluster", name)
		_, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(name, metav1.GetOptions{})
		require.NoError(t, err)
	}

	// * I should see service accounts for etcd with permissions matching what’s listed in the ClusterServiceVersion
	for _, accountName := range []string{"etcd-operator"} {
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

	for _, deploymentName := range []string{"etcd-operator"} {
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

	etcdCluster := map[string]interface{}{
		"kind":       "EtcdCluster",
		"apiVersion": "etcd.database.coreos.com/v1beta2",
		"metadata": map[string]interface{}{
			"name":      "test-etcd",
			"namespace": testNamespace,
			"labels":    map[string]interface{}{"etcd_cluster": "test-etcd"},
		},
		"spec": map[string]interface{}{
			"size":    expectedEtcdNodes,
			"version": etcdVersion,
		},
	}

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: etcdCluster})
	require.NoError(t, err)

	require.NoError(t, pollForCustomResource(t, c, "etcd.database.coreos.com", "v1beta2", "EtcdCluster", "test-etcd"))

	etcdPods, err := awaitPods(t, c, "etcd_cluster=test-etcd", expectedEtcdNodes)
	require.NoError(t, err)
	require.Equal(t, expectedEtcdNodes, len(etcdPods.Items))
}

func TestInstallPrometheusOCS(t *testing.T) {
	c := newKubeClient(t)

	catalogSource, err := fetchCatalogSource(t, c, ocsConfigMap)
	require.NoError(t, err)
	require.NotNil(t, catalogSource)
	inMem, err := registry.NewInMemoryFromConfigMap(c, testNamespace, catalogSource.Spec.ConfigMap)
	require.NoError(t, err)
	require.NotNil(t, inMem)
	latestPrometheusCSV, err := inMem.FindCSVForPackageNameUnderChannel("prometheus", "alpha")
	require.NoError(t, err)
	require.NotNil(t, latestPrometheusCSV)

	prometheusInstallPlan := v1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.InstallPlanKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-verify-" + latestPrometheusCSV.Name,
			Namespace: testNamespace,
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{latestPrometheusCSV.Name},
			Approval:                   v1alpha1.ApprovalAutomatic,
		},
	}

	// Create a new installplan for etcd
	prometheusUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&prometheusInstallPlan)
	require.NoError(t, err)

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: prometheusUnst})
	require.NoError(t, err)

	// Get InstallPlan and verify status
	fetchedInstallPlan, err := fetchInstallPlan(t, c, prometheusInstallPlan.GetName(), installPlanCompleteChecker)

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Ensure CustomResourceDefinitions and ClusterServiceVersion-v1s are present in the resolved InstallPlan
	requiredCSVs := []string{"prometheusoperator"}
	requiredCRDs := []string{"prometheuses.monitoring.coreos.com", "alertmanagers.monitoring.coreos.com", "servicemonitors.monitoring.coreos.com"}

	csvNames := map[string]string{}
	crdNames := []string{}

	for _, step := range fetchedInstallPlan.Status.Plan {
		if step.Resource.Kind == "CustomResourceDefinition" {
			crdNames = append(crdNames, step.Resource.Name)
		} else if step.Resource.Kind == v1alpha1.ClusterServiceVersionKind {
			csvNames[strings.Split(step.Resource.Name, ".")[0]] = step.Resource.Name
		}
	}

	// Check that the CSV and CRDs are actually present in the cluster as well
	for _, name := range requiredCSVs {
		require.NotEmpty(t, csvNames[name])

		t.Logf("Ensuring CSV %s is present in %s namespace", name, testNamespace)
		_, err := c.GetCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, testNamespace, v1alpha1.ClusterServiceVersionKind, csvNames[name])
		require.NoError(t, err)
	}

	for _, name := range requiredCRDs {
		require.Contains(t, crdNames, name)

		t.Logf("Ensuring CRD %s is present in cluster", name)
		_, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(name, metav1.GetOptions{})
		require.NoError(t, err)
	}

	// * I should see service accounts for Prometheus with permissions matching what’s listed in the ClusterServiceVersion
	for _, accountName := range []string{"prometheus-operator-0-22-1"} {
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

	for _, deploymentName := range []string{"prometheus-operator"} {
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

	prometheus := map[string]interface{}{
		"kind":       "Prometheus",
		"apiVersion": "monitoring.coreos.com/v1",
		"metadata": map[string]interface{}{
			"name":      "test-prometheus",
			"namespace": testNamespace,
			"labels":    map[string]interface{}{"prometheus": "test-prometheus"},
		},
		"spec": map[string]interface{}{
			"replicas": expectedPrometheusSize,
			"version":  prometheusVersion,
		},
	}

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: prometheus})
	require.NoError(t, err)

	require.NoError(t, pollForCustomResource(t, c, "monitoring.coreos.com", "v1", "Prometheus", "test-prometheus"))

	prometheusPods, err := awaitPods(t, c, "prometheus=test-prometheus", expectedPrometheusSize)
	require.NoError(t, err)
	require.Equal(t, expectedPrometheusSize, len(prometheusPods.Items))
}

// As an infra owner, when I create an installplan for Vault:
// * I should see a resolved installplan listing EtcdCluster, VaultService, Vault ClusterServiceVersion, and etcd ClusterServiceVersion
// * I should see the resolved resources be created in the same namespace.
// * I should see a vault-operator deployment and an etcd-operator deployment in the same namespace
// * I should see service accounts for vault and etcd with permissions matching what’s listed in the ClusterServiceVersions
// * When I create a VaultService CR
//   * I should see a related EtcdCluster CR appear
//   * I should see pods for Vault and etcd appear
func TestInstallVaultOCS(t *testing.T) {
	c := newKubeClient(t)

	catalogSource, err := fetchCatalogSource(t, c, ocsConfigMap)
	require.NoError(t, err)
	require.NotNil(t, catalogSource)
	inMem, err := registry.NewInMemoryFromConfigMap(c, testNamespace, catalogSource.Spec.ConfigMap)
	require.NoError(t, err)
	require.NotNil(t, inMem)
	latestVaultCSV, err := inMem.FindCSVForPackageNameUnderChannel("vault", "alpha")
	require.NoError(t, err)
	require.NotNil(t, latestVaultCSV)

	vaultInstallPlan := v1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.InstallPlanKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-verify-" + latestVaultCSV.Name,
			Namespace: testNamespace,
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{latestVaultCSV.Name},
			Approval:                   v1alpha1.ApprovalAutomatic,
		},
	}

	// Create a new installplan for vault
	vaultUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&vaultInstallPlan)
	require.NoError(t, err)

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: vaultUnst})
	require.NoError(t, err)

	// Get InstallPlan and verify status
	fetchedInstallPlan, err := fetchInstallPlan(t, c, vaultInstallPlan.GetName(), installPlanCompleteChecker)

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Ensure CustomResourceDefinitions and ClusterServiceVersion-v1s are present in the resolved InstallPlan
	requiredCSVs := []string{"etcdoperator", "vault-operator"}
	requiredCRDs := []string{"etcdclusters.etcd.database.coreos.com", "vaultservices.vault.security.coreos.com"}

	csvNames := map[string]string{}
	crdNames := []string{}

	for _, step := range fetchedInstallPlan.Status.Plan {
		if step.Resource.Kind == "CustomResourceDefinition" {
			crdNames = append(crdNames, step.Resource.Name)
		} else if step.Resource.Kind == v1alpha1.ClusterServiceVersionKind {
			csvNames[strings.Split(step.Resource.Name, ".")[0]] = step.Resource.Name
		}
	}

	// Check that the CSV and CRDs are actually present in the cluster as well
	for _, name := range requiredCSVs {
		require.NotEmpty(t, csvNames[name])

		t.Logf("Ensuring CSV %s is present in %s namespace", name, testNamespace)
		_, err := c.GetCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, testNamespace, v1alpha1.ClusterServiceVersionKind, csvNames[name])
		require.NoError(t, err)
	}

	for _, name := range requiredCRDs {
		require.Contains(t, crdNames, name)

		t.Logf("Ensuring CRD %s is present in cluster", name)
		_, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(name, metav1.GetOptions{})
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
