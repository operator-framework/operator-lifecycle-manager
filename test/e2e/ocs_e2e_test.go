package e2e

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestInstallEtcdOCS(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	catalogSource, err := fetchCatalogSource(t, crc, ocsConfigMap, operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	require.NotNil(t, catalogSource)
	inMem, err := registry.NewInMemoryFromConfigMap(c, operatorNamespace, catalogSource.Spec.ConfigMap)
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
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, etcdInstallPlan.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Ensure CustomResourceDefinitions and ClusterServiceVersions are present in the resolved InstallPlan
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
		var deployment *appsv1.Deployment
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
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	catalogSource, err := fetchCatalogSource(t, crc, ocsConfigMap, operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	require.NotNil(t, catalogSource)
	inMem, err := registry.NewInMemoryFromConfigMap(c, operatorNamespace, catalogSource.Spec.ConfigMap)
	require.NoError(t, err)
	require.NotNil(t, inMem)
	latestPrometheusCSV, err := inMem.FindCSVForPackageNameUnderChannel("prometheus", "preview")
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
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, prometheusInstallPlan.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Ensure CustomResourceDefinitions and ClusterServiceVersions are present in the resolved InstallPlan
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
	for _, accountName := range []string{"prometheus-operator-0-22-2"} {
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
		var deployment *appsv1.Deployment
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
			"replicas":        expectedPrometheusSize,
			"version":         prometheusVersion,
			"securityContext": struct{}{},
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"memory": "10Mi",
				},
			},
		},
	}

	t.Run("test prometheus object creation", func(t *testing.T) {
		err = c.CreateCustomResource(&unstructured.Unstructured{Object: prometheus})
		require.NoError(t, err)

		require.NoError(t, pollForCustomResource(t, c, "monitoring.coreos.com", "v1", "Prometheus", "test-prometheus"))

		prometheusPods, err := awaitPods(t, c, "prometheus=test-prometheus", expectedPrometheusSize)
		require.NoError(t, err)
		require.Equal(t, expectedPrometheusSize, len(prometheusPods.Items))
	})
}
