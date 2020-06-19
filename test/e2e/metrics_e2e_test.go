// +build !bare

package e2e

import (
	"context"
	"fmt"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Metrics are generated for OLM managed resources", func() {

	var (
		c   operatorclient.ClientInterface
		crc versioned.Interface
	)

	BeforeEach(func() {
		c = newKubeClient()
		crc = newCRClient()

	})

	Context("Given an OperatorGroup that supports all namespaces", func() {
		By("using the default OperatorGroup created in BeforeSuite")
		When("a CSV spec does not include Install Mode", func() {

			var (
				cleanupCSV cleanupFunc
				failingCSV v1alpha1.ClusterServiceVersion
			)

			BeforeEach(func() {

				failingCSV = v1alpha1.ClusterServiceVersion{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1alpha1.ClusterServiceVersionKind,
						APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: genName("failing-csv-test-"),
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						InstallStrategy: v1alpha1.NamedInstallStrategy{
							StrategyName: v1alpha1.InstallStrategyNameDeployment,
							StrategySpec: strategy,
						},
					},
				}

				var err error
				cleanupCSV, err = createCSV(c, crc, failingCSV, testNamespace, false, false)
				Expect(err).ToNot(HaveOccurred())

				_, err = fetchCSV(crc, failingCSV.Name, testNamespace, csvFailedChecker)
				Expect(err).ToNot(HaveOccurred())
			})

			It("generates csv_abnormal metric for OLM pod", func() {

				// Verify metrics have been emitted for packageserver csv
				Expect(getMetricsFromPod(c, getPodWithLabel(c, "app=olm-operator"), "8081")).To(And(
					ContainSubstring("csv_abnormal"),
					ContainSubstring(fmt.Sprintf("name=\"%s\"", failingCSV.Name)),
					ContainSubstring("phase=\"Failed\""),
					ContainSubstring("reason=\"UnsupportedOperatorGroup\""),
					ContainSubstring("version=\"0.0.0\""),
					ContainSubstring("csv_succeeded"),
				))

				cleanupCSV()
			})

			When("the failed CSV is deleted", func() {

				BeforeEach(func() {
					if cleanupCSV != nil {
						cleanupCSV()
					}
				})

				It("deletes its associated CSV metrics", func() {
					// Verify that when the csv has been deleted, it deletes the corresponding CSV metrics
					Expect(getMetricsFromPod(c, getPodWithLabel(c, "app=olm-operator"), "8081")).ToNot(And(
						ContainSubstring("csv_abnormal{name=\"%s\"", failingCSV.Name),
						ContainSubstring("csv_succeeded{name=\"%s\"", failingCSV.Name),
					))
				})
			})
		})
	})

	Context("Subscription Metric", func() {
		var (
			subscriptionCleanup cleanupFunc
			subscription        *v1alpha1.Subscription
		)
		When("A subscription object is created", func() {

			BeforeEach(func() {
				subscriptionCleanup, _ = createSubscription(GinkgoT(), crc, testNamespace, "metric-subscription-for-create", testPackageName, stableChannel, v1alpha1.ApprovalManual)
			})

			It("generates subscription_sync_total metric", func() {

				// Verify metrics have been emitted for subscription
				Eventually(func() string {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}, time.Minute, 5*time.Second).Should(And(
					ContainSubstring("subscription_sync_total"),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.NAME_LABEL, "metric-subscription-for-create")),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, stableChannel)),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.PACKAGE_LABEL, testPackageName))))
			})
			if subscriptionCleanup != nil {
				subscriptionCleanup()
			}
		})
		When("A subscription object is updated", func() {

			BeforeEach(func() {
				subscriptionCleanup, subscription = createSubscription(GinkgoT(), crc, testNamespace, "metric-subscription-for-update", testPackageName, stableChannel, v1alpha1.ApprovalManual)
				subscription.Spec.Channel = "beta"
				updateSubscription(GinkgoT(), crc, subscription)
			})

			It("deletes the old Subscription metric and emits the new metric", func() {
				Eventually(func() string {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}, time.Minute, 5*time.Second).ShouldNot(And(
					ContainSubstring("subscription_sync_total{name=\"metric-subscription-for-update\""),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, stableChannel))))

				Eventually(func() string {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}, time.Minute, 5*time.Second).Should(And(
					ContainSubstring("subscription_sync_total"),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.NAME_LABEL, "metric-subscription-for-update")),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, "beta")),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.PACKAGE_LABEL, testPackageName))))
			})
			if subscriptionCleanup != nil {
				subscriptionCleanup()
			}
		})

		When("A subscription object is deleted", func() {

			BeforeEach(func() {
				subscriptionCleanup, subscription = createSubscription(GinkgoT(), crc, testNamespace, "metric-subscription-for-delete", testPackageName, stableChannel, v1alpha1.ApprovalManual)
				if subscriptionCleanup != nil {
					subscriptionCleanup()
				}
			})

			It("deletes the Subscription metric", func() {
				Eventually(func() string {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}, time.Minute, 5*time.Second).ShouldNot(ContainSubstring("subscription_sync_total{name=\"metric-subscription-for-update\""))
			})
		})
	})
})

func getPodWithLabel(client operatorclient.ClientInterface, label string) *corev1.Pod {
	listOptions := metav1.ListOptions{LabelSelector: label}
	var podList *corev1.PodList
	Eventually(func() (err error) {
		podList, err = client.KubernetesInterface().CoreV1().Pods(operatorNamespace).List(context.TODO(), listOptions)
		return
	}).Should(Succeed(), "Failed to list OLM pods")
	Expect(len(podList.Items)).To(Equal(1))

	return &podList.Items[0]
}

func getMetricsFromPod(client operatorclient.ClientInterface, pod *corev1.Pod, port string) string {
	By(fmt.Sprintf("querying pod %s/%s", pod.GetNamespace(), pod.GetName()))

	// assuming -tls-cert and -tls-key aren't used anywhere else as a parameter value
	var foundCert, foundKey bool
	for _, arg := range pod.Spec.Containers[0].Args {
		matched, err := regexp.MatchString(`^-?-tls-cert`, arg)
		Expect(err).ToNot(HaveOccurred())
		foundCert = foundCert || matched

		matched, err = regexp.MatchString(`^-?-tls-key`, arg)
		Expect(err).ToNot(HaveOccurred())
		foundKey = foundKey || matched
	}

	var scheme string
	if foundCert && foundKey {
		scheme = "https"
	} else {
		scheme = "http"
	}
	ctx.Ctx().Logf("Retrieving metrics using scheme %v\n", scheme)

	var raw []byte
	Eventually(func() (err error) {
		raw, err = client.KubernetesInterface().CoreV1().RESTClient().Get().
			Namespace(pod.GetNamespace()).
			Resource("pods").
			SubResource("proxy").
			Name(net.JoinSchemeNamePort(scheme, pod.GetName(), port)).
			Suffix("metrics").
			Do(context.Background()).Raw()
		return
	}).Should(Succeed())

	return string(raw)
}
