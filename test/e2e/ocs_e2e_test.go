package e2e

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestInstallEtcdOCS(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	etcdSubscription := v1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.SubscriptionKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-validate",
			Namespace: testNamespace,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			CatalogSource:          ocsConfigMap,
			CatalogSourceNamespace: operatorNamespace,
			Package:                "etcd",
			Channel:                "alpha",
		},
	}

	sub, err := crc.OperatorsV1alpha1().Subscriptions(testNamespace).Create(&etcdSubscription)
	require.NoError(t, err)
	defer buildSubscriptionCleanupFunc(t, crc, sub)()

	sub, err = fetchSubscription(t, crc, testNamespace, "etcd-validate", subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	_, err = fetchCSV(t, crc, sub.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded))
	require.NoError(t, err)

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
	etcdSubscription := v1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.SubscriptionKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prometheus-validate",
			Namespace: testNamespace,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			CatalogSource:          ocsConfigMap,
			CatalogSourceNamespace: operatorNamespace,
			Package:                "prometheus",
			Channel:                "preview",
		},
	}

	sub, err := crc.OperatorsV1alpha1().Subscriptions(testNamespace).Create(&etcdSubscription)
	require.NoError(t, err)
	defer buildSubscriptionCleanupFunc(t, crc, sub)()

	sub, err = fetchSubscription(t, crc, testNamespace, "prometheus-validate", subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	_, err = fetchCSV(t, crc, sub.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded))
	require.NoError(t, err)

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
