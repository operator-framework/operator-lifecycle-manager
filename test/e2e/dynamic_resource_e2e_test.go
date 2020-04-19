package e2e

import (
	"context"
	"strings"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	. "github.com/onsi/ginkgo"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Dynamic Resource", func() {
	It("resolve prometheus API", func() {
		Skip("this test disabled pending fix of the v1 CRD feature")
		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())
		dynamicClient := ctx.Ctx().DynamicClient()

		ns, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("ns-"),
			},
		}, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		deleteOpts := &metav1.DeleteOptions{}
		defer func() {
			require.NoError(GinkgoT(), c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), ns.GetName(), *deleteOpts))
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

		catsrc, err = crc.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Create(context.TODO(), catsrc, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Delete(context.TODO(), catsrc.GetName(), *deleteOpts))
		}()

		// Wait for the CatalogSource to be ready
		catsrc, err = fetchCatalogSource(GinkgoT(), crc, catsrc.GetName(), catsrc.GetNamespace(), catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		// Generate a Subscription
		subName := genName("dynamic-resources")
		cleanupSub := createSubscriptionForCatalog(GinkgoT(), crc, catsrc.GetNamespace(), subName, catsrc.GetName(), "etcd", "singlenamespace-alpha", "", v1alpha1.ApprovalAutomatic)
		defer cleanupSub()

		sub, err := fetchSubscription(GinkgoT(), crc, catsrc.GetNamespace(), subName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)

		// Wait for the expected InstallPlan's execution to either fail or succeed
		ipName := sub.Status.InstallPlanRef.Name
		ip, err := waitForInstallPlan(GinkgoT(), crc, ipName, sub.GetNamespace(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed, v1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		// Ensure the InstallPlan contains the steps resolved from the bundle image
		expectedSteps := map[registry.ResourceKey]struct{}{
			{Name: "my-prometheusrule", Kind: "PrometheusRule"}: {},
			{Name: "my-servicemonitor", Kind: "ServiceMonitor"}: {},
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
		require.Lenf(GinkgoT(), expectedSteps, 0, "Resource steps do not match expected: %#v", expectedSteps)

		// Confirm that the expected types exist
		gvr := schema.GroupVersionResource{
			Group:    "monitoring.coreos.com",
			Version:  "v1",
			Resource: "prometheusrules",
		}

		err = waitForGVR(dynamicClient, gvr, "my-prometheusrule", ns.GetName())
		require.NoError(GinkgoT(), err)

		gvr.Resource = "servicemonitors"
		err = waitForGVR(dynamicClient, gvr, "my-servicemonitor", ns.GetName())
		require.NoError(GinkgoT(), err)
	})
})
