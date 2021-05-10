// +build !bare

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
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

				Expect(getMetricsFromPod(c, getPodWithLabel(c, "app=olm-operator"), "8081")).To(And(
					ContainElement(LikeMetric(
						WithFamily("csv_abnormal"),
						WithName(failingCSV.Name),
						WithPhase("Failed"),
						WithReason("UnsupportedOperatorGroup"),
						WithVersion("0.0.0"),
					)),
					ContainElement(LikeMetric(
						WithFamily("csv_succeeded"),
						WithValue(0),
						WithName(failingCSV.Name),
					)),
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
						ContainElement(LikeMetric(WithFamily("csv_abnormal"), WithName(failingCSV.Name))),
						ContainElement(LikeMetric(WithFamily("csv_succeeded"), WithName(failingCSV.Name))),
					))
				})
			})
		})
	})

	Context("Metrics emitted by objects during operator installation", func() {
		var (
			subscriptionCleanup cleanupFunc
			subscription        *v1alpha1.Subscription
		)

		When("A subscription object is created", func() {
			BeforeEach(func() {
				subscriptionCleanup, _ = createSubscription(GinkgoT(), crc, testNamespace, "metric-subscription-for-create", testPackageName, stableChannel, v1alpha1.ApprovalManual)
			})

			AfterEach(func() {
				if subscriptionCleanup != nil {
					subscriptionCleanup()
				}
			})

			It("generates subscription_sync_total metric", func() {

				// Verify metrics have been emitted for subscription
				Eventually(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}).Should(ContainElement(LikeMetric(
					WithFamily("subscription_sync_total"),
					WithName("metric-subscription-for-create"),
					WithChannel(stableChannel),
					WithPackage(testPackageName),
					WithApproval(string(v1alpha1.ApprovalManual)),
				)))
			})

			It("generates dependency_resolution metric", func() {

				// Verify metrics have been emitted for dependency resolution
				Eventually(func() bool {
					return Eventually(func() []Metric {
						return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
					}).Should(ContainElement(LikeMetric(
						WithFamily("olm_resolution_duration_seconds"),
						WithLabel("outcome", "failed"),
						WithValueGreaterThan(0),
					)))
				})
			})
		})

		When("A subscription object is updated after emitting metrics", func() {

			BeforeEach(func() {
				subscriptionCleanup, subscription = createSubscription(GinkgoT(), crc, testNamespace, "metric-subscription-for-update", testPackageName, stableChannel, v1alpha1.ApprovalManual)
				Eventually(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}).Should(ContainElement(LikeMetric(WithFamily("subscription_sync_total"), WithLabel("name", "metric-subscription-for-update"))))
				Eventually(func() error {
					s, err := crc.OperatorsV1alpha1().Subscriptions(subscription.GetNamespace()).Get(context.TODO(), subscription.GetName(), metav1.GetOptions{})
					if err != nil {
						return err
					}
					s.Spec.Channel = betaChannel
					_, err = crc.OperatorsV1alpha1().Subscriptions(s.GetNamespace()).Update(context.TODO(), s, metav1.UpdateOptions{})
					return err
				}).Should(Succeed())
			})

			AfterEach(func() {
				if subscriptionCleanup != nil {
					subscriptionCleanup()
				}
			})

			It("deletes the old Subscription metric and emits the new metric", func() {
				Eventually(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}).Should(And(
					Not(ContainElement(LikeMetric(
						WithFamily("subscription_sync_total"),
						WithName("metric-subscription-for-update"),
						WithChannel(stableChannel),
						WithPackage(testPackageName),
						WithApproval(string(v1alpha1.ApprovalManual)),
					))),
					ContainElement(LikeMetric(
						WithFamily("subscription_sync_total"),
						WithName("metric-subscription-for-update"),
						WithChannel(betaChannel),
						WithPackage(testPackageName),
						WithApproval(string(v1alpha1.ApprovalManual)),
					)),
				))
			})
			When("The subscription object is updated again", func() {

				BeforeEach(func() {
					Eventually(func() error {
						s, err := crc.OperatorsV1alpha1().Subscriptions(subscription.GetNamespace()).Get(context.TODO(), subscription.GetName(), metav1.GetOptions{})
						if err != nil {
							return err
						}
						s.Spec.Channel = alphaChannel
						_, err = crc.OperatorsV1alpha1().Subscriptions(s.GetNamespace()).Update(context.TODO(), s, metav1.UpdateOptions{})
						return err
					}).Should(Succeed())
				})

				It("deletes the old subscription metric and emits the new metric(there is only one metric for the subscription)", func() {
					Eventually(func() []Metric {
						return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
					}).Should(And(
						Not(ContainElement(LikeMetric(
							WithFamily("subscription_sync_total"),
							WithName("metric-subscription-for-update"),
							WithChannel(stableChannel),
						))),
						Not(ContainElement(LikeMetric(
							WithFamily("subscription_sync_total"),
							WithName("metric-subscription-for-update"),
							WithChannel(betaChannel),
							WithPackage(testPackageName),
							WithApproval(string(v1alpha1.ApprovalManual)),
						))),
						ContainElement(LikeMetric(
							WithFamily("subscription_sync_total"),
							WithName("metric-subscription-for-update"),
							WithChannel(alphaChannel),
							WithPackage(testPackageName),
							WithApproval(string(v1alpha1.ApprovalManual)),
						))))
				})
			})
		})

		When("A subscription object is deleted after emitting metrics", func() {

			BeforeEach(func() {
				subscriptionCleanup, subscription = createSubscription(GinkgoT(), crc, testNamespace, "metric-subscription-for-delete", testPackageName, stableChannel, v1alpha1.ApprovalManual)
				Eventually(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}).Should(ContainElement(LikeMetric(WithFamily("subscription_sync_total"), WithLabel("name", "metric-subscription-for-delete"))))
				if subscriptionCleanup != nil {
					subscriptionCleanup()
					subscriptionCleanup = nil
				}
			})

			AfterEach(func() {
				if subscriptionCleanup != nil {
					subscriptionCleanup()
				}
			})

			It("deletes the Subscription metric", func() {
				Eventually(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}).ShouldNot(ContainElement(LikeMetric(WithFamily("subscription_sync_total"), WithName("metric-subscription-for-delete"))))
			})
		})
	})

	Context("Metrics emitted by CatalogSources", func() {
		When("A valid CatalogSource object is created", func() {
			var (
				name        = "metrics-catsrc-valid"
				cleanup     func()
				cleanupDone = false
			)
			BeforeEach(func() {
				mainPackageName := genName("nginx-")

				mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)

				stableChannel := "stable"

				mainCRD := newCRD(genName("ins-"))
				mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, nil)

				mainManifests := []registry.PackageManifest{
					{
						PackageName: mainPackageName,
						Channels: []registry.PackageChannel{
							{Name: stableChannel, CurrentCSVName: mainPackageStable},
						},
						DefaultChannelName: stableChannel,
					},
				}
				_, cleanup = createInternalCatalogSource(c, crc, name, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV})
			})
			AfterEach(func() {
				if !cleanupDone {
					cleanup()
				}
			})
			It("emits metrics for the catalogSource", func() {
				Eventually(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}).Should(And(
					ContainElement(LikeMetric(
						WithFamily("catalog_source_count"),
						WithValueGreaterThan(0),
					)),
					ContainElement(LikeMetric(
						WithFamily("catalogSource_ready"),
						WithName(name),
						WithNamespace(testNamespace),
						WithValue(1),
					)),
				))
			})
			When("The CatalogSource object is deleted", func() {
				BeforeEach(func() {
					cleanup()
					cleanupDone = true
				})
				It("deletes the metrics for the CatalogSource", func() {
					Eventually(func() []Metric {
						return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
					}).Should(And(
						Not(ContainElement(LikeMetric(
							WithFamily("catalogSource_ready"),
							WithName(name),
							WithNamespace(testNamespace),
						)))))
				})
			})
		})

		When("A CatalogSource object is in an invalid state", func() {
			var (
				name    = "metrics-catsrc-invalid"
				cleanup func()
			)
			BeforeEach(func() {
				_, cleanup = createInvalidGRPCCatalogSource(crc, name, testNamespace)
			})
			AfterEach(func() {
				cleanup()
			})
			It("emits metrics for the CatlogSource with a Value greater than 0", func() {
				Eventually(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}).Should(And(
					ContainElement(LikeMetric(
						WithFamily("catalogSource_ready"),
						WithName(name),
						WithNamespace(testNamespace),
						WithValue(0),
					)),
				))
				Consistently(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"), "8081")
				}, "3m").Should(And(
					ContainElement(LikeMetric(
						WithFamily("catalogSource_ready"),
						WithName(name),
						WithNamespace(testNamespace),
						WithValue(0),
					)),
				))
			})
		})
	})
})

func getPodWithLabel(client operatorclient.ClientInterface, label string) *corev1.Pod {
	listOptions := metav1.ListOptions{LabelSelector: label}
	var podList *corev1.PodList
	EventuallyWithOffset(1, func() (numPods int, err error) {
		podList, err = client.KubernetesInterface().CoreV1().Pods(operatorNamespace).List(context.TODO(), listOptions)
		if podList != nil {
			numPods = len(podList.Items)
		}

		return
	}).Should(Equal(1), "number of pods never scaled to one")

	return &podList.Items[0]
}

func getMetricsFromPod(client operatorclient.ClientInterface, pod *corev1.Pod, port string) []Metric {
	ctx.Ctx().Logf("querying pod %s/%s\n", pod.GetNamespace(), pod.GetName())

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

	mfs := make(map[string]*io_prometheus_client.MetricFamily)
	EventuallyWithOffset(1, func() error {
		raw, err := client.KubernetesInterface().CoreV1().RESTClient().Get().
			Namespace(pod.GetNamespace()).
			Resource("pods").
			SubResource("proxy").
			Name(net.JoinSchemeNamePort(scheme, pod.GetName(), port)).
			Suffix("metrics").
			Do(context.Background()).Raw()
		if err != nil {
			return err
		}
		var p expfmt.TextParser
		mfs, err = p.TextToMetricFamilies(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		return nil
	}).Should(Succeed())

	var metrics []Metric
	for family, mf := range mfs {
		var ignore bool
		for _, ignoredPrefix := range []string{"go_", "process_", "promhttp_"} {
			ignore = ignore || strings.HasPrefix(family, ignoredPrefix)
		}
		if ignore {
			// Metrics with these prefixes shouldn't be
			// relevant to these tests, so they can be
			// stripped out to make test failures easier
			// to understand.
			continue
		}

		for _, metric := range mf.Metric {
			m := Metric{
				Family: family,
			}
			if len(metric.GetLabel()) > 0 {
				m.Labels = make(map[string][]string)
			}
			for _, pair := range metric.GetLabel() {
				m.Labels[pair.GetName()] = append(m.Labels[pair.GetName()], pair.GetValue())
			}
			if u := metric.GetUntyped(); u != nil {
				m.Value = u.GetValue()
			}
			if g := metric.GetGauge(); g != nil {
				m.Value = g.GetValue()
			}
			if c := metric.GetCounter(); c != nil {
				m.Value = c.GetValue()
			}
			metrics = append(metrics, m)
		}
	}
	return metrics
}
