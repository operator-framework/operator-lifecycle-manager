// +build !bare

package e2e

import (
	"context"
	"fmt"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

var _ = Describe("Metrics", func() {

	var (
		c   operatorclient.ClientInterface
		crc versioned.Interface
	)

	BeforeEach(func() {
		c = newKubeClient()
		crc = newCRClient()

	})

	Context("CSV metrics", func() {
		It("endpoint", func() {

			// TestMetrics tests the metrics endpoint of the OLM pod.

			failingCSV := v1alpha1.ClusterServiceVersion{
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

			cleanupCSV, err := createCSV(GinkgoT(), c, crc, failingCSV, testNamespace, false, false)
			Expect(err).ToNot(HaveOccurred())

			_, err = fetchCSV(GinkgoT(), crc, failingCSV.Name, testNamespace, csvFailedChecker)
			Expect(err).ToNot(HaveOccurred())

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

			// Verify that when the csv has been deleted, it deletes the corresponding CSV metrics
			Expect(getMetricsFromPod(c, getPodWithLabel(c, "app=olm-operator"), "8081")).ToNot(And(
				ContainSubstring("csv_abnormal{name=\"%s\"", failingCSV.Name),
				ContainSubstring("csv_succeeded{name=\"%s\"", failingCSV.Name),
			))
		})
	})

	Context("Subscription Metric", func() {
		var (
			subscriptionCleanup cleanupFunc
			subscription        *v1alpha1.Subscription
		)
		When("A subscription object is created", func() {

			BeforeEach(func() {
				subscriptionCleanup, subscription = createSubscription(GinkgoT(), crc, testNamespace, "metric-subscription", testPackageName, stableChannel, v1alpha1.ApprovalManual)
			})

			It("generates subscription_sync_total metric", func() {

				// Verify metrics have been emitted for subscription
				Eventually(func() string {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}, time.Minute, 5*time.Second).Should(And(
					ContainSubstring("subscription_sync_total"),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.NAME_LABEL, "metric-subscription")),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, stableChannel)),
					ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.PACKAGE_LABEL, testPackageName))))
			})

			When("The subscription object is updated", func() {

				BeforeEach(func() {
					updatedSubscription, err := crc.OperatorsV1alpha1().Subscriptions(subscription.GetNamespace()).Get(context.TODO(), subscription.GetName(), metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())

					Eventually(Apply(updatedSubscription, func(s *v1alpha1.Subscription) error {
						s.Spec.Channel = betaChannel
						return nil
					})).Should(Succeed(), "could not update subscription")
				})

				It("deletes the old Subscription metric and emits the new metric", func() {
					Eventually(func() string {
						return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
					}, time.Minute, 5*time.Second).ShouldNot(And(
						ContainSubstring("subscription_sync_total{name=\"metric-subscription\""),
						ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, stableChannel))))

					Eventually(func() string {
						return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
					}, time.Minute, 5*time.Second).Should(And(
						ContainSubstring("subscription_sync_total"),
						ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.NAME_LABEL, "metric-subscription")),
						ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, betaChannel)),
						ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.PACKAGE_LABEL, testPackageName))))
				})

				When("The Subscription object is updated again", func() {

					BeforeEach(func() {
						updatedSubscription, err := crc.OperatorsV1alpha1().Subscriptions(subscription.GetNamespace()).Get(context.TODO(), subscription.GetName(), metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())

						Eventually(Apply(updatedSubscription, func(s *v1alpha1.Subscription) error {
							s.Spec.Channel = alphaChannel
							return nil
						})).Should(Succeed(), "could not update subscription")

					})

					It("deletes the old subscription metric and emits the new metric(there is only one metric for the subscription)", func() {
						Eventually(func() string {
							return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
						}, time.Minute, 5*time.Second).ShouldNot(And(
							ContainSubstring("subscription_sync_total{name=\"metric-subscription-for-update\""),
							ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, stableChannel)),
							ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, betaChannel))))

						Eventually(func() string {
							return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
						}, time.Minute, 5*time.Second).Should(And(
							ContainSubstring("subscription_sync_total"),
							ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.NAME_LABEL, "metric-subscription")),
							ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, alphaChannel)),
							ContainSubstring(fmt.Sprintf("%s=\"%s\"", metrics.PACKAGE_LABEL, testPackageName))))
					})
				})
			})

			AfterEach(func() {
				if subscriptionCleanup != nil {
					subscriptionCleanup()
				}
			})
		})
		When("A Subscription object is deleted", func() {

			BeforeEach(func() {
				subscriptionCleanup, _ = createSubscription(GinkgoT(), crc, testNamespace, "metric-subscription-for-deletion", testPackageName, stableChannel, v1alpha1.ApprovalManual)
				if subscriptionCleanup != nil {
					subscriptionCleanup()
				}
			})
			It("deletes the Subscription metric", func() {
				Eventually(func() string {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}, time.Minute, 5*time.Second).ShouldNot(ContainSubstring("subscription_sync_total{name=\"metric-subscription-for-deletion\""))
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
	log.Infof("Retrieving metrics using scheme %v\n", scheme)

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
