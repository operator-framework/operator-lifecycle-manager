package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

func TestDynamicResourcesResolvePrometheusAPI(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	config, err := clientcmd.BuildConfigFromFlags("", *kubeConfigPath)
	require.NoError(t, err)

	c := newKubeClient(t)
	crc := newCRClient(t)
	dynamicClient := newDynamicClient(t, config)

	ns, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("ns-"),
		},
	})
	require.NoError(t, err)

	deleteOpts := &metav1.DeleteOptions{}
	defer func() {
		require.NoError(t, c.KubernetesInterface().CoreV1().Namespaces().Delete(ns.GetName(), deleteOpts))
	}()

	catsrc := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("dynamic-catalog-"),
			Namespace: ns.GetName(),
		},
		Spec: v1alpha1.CatalogSourceSpec{
			Image:      "quay.io/olmtest/catsrc_dynamic_resources:e2e-test",
			SourceType: v1alpha1.SourceTypeGrpc,
		},
	}

	catsrc, err = crc.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Create(catsrc)
	require.NoError(t, err)

	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Delete(catsrc.GetName(), deleteOpts))
	}()

	// Wait for the CatalogSource to be ready
	catsrc, err = fetchCatalogSource(t, crc, catsrc.GetName(), catsrc.GetNamespace(), catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Generate a Subscription
	subName := genName("dynamic-resources")
	cleanupSub := createSubscriptionForCatalog(t, crc, catsrc.GetNamespace(), subName, catsrc.GetName(), "etcd", "singlenamespace-alpha", "", v1alpha1.ApprovalAutomatic)
	defer cleanupSub()

	sub, err := fetchSubscription(t, crc, catsrc.GetNamespace(), subName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)

	// Wait for the expected InstallPlan's execution to either fail or succeed
	ipName := sub.Status.InstallPlanRef.Name
	ip, err := waitForInstallPlan(t, crc, ipName, sub.GetNamespace(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed, v1alpha1.InstallPlanPhaseComplete))
	require.NoError(t, err)
	// Ensure the InstallPlan contains the steps resolved from the bundle image
	expectedSteps := map[registry.ResourceKey]struct{}{
		registry.ResourceKey{Name: "my-prometheusrule", Kind: "PrometheusRule"}: {},
		registry.ResourceKey{Name: "my-servicemonitor", Kind: "ServiceMonitor"}: {},
	}

	for _, step := range ip.Status.Plan {
		key := registry.ResourceKey{
			Name: step.Resource.Name,
			Kind: step.Resource.Kind,
		}
		for expected := range expectedSteps {
			if strings.HasPrefix(key.Name, expected.Name) && key.Kind == expected.Kind {
				delete(expectedSteps, expected)
			}
		}
	}
	require.Lenf(t, expectedSteps, 0, "Resource steps do not match expected: %#v", expectedSteps)

	// Confirm that the expected types exist
	gvr := schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "prometheusrules",
	}

	err = waitForGVR(dynamicClient, gvr, "my-prometheusrule", ns.GetName())
	require.NoError(t, err)

	gvr.Resource = "servicemonitors"
	err = waitForGVR(dynamicClient, gvr, "my-servicemonitor", ns.GetName())
	require.NoError(t, err)
}
