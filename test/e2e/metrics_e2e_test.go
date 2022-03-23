//go:build !bare
// +build !bare

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	appsv1 "k8s.io/api/apps/v1"
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
		BeforeEach(func() {
			By("using the default OperatorGroup created in BeforeSuite")
		})

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

				Expect(getMetricsFromPod(c, getPodWithLabel(c, "app=olm-operator"))).To(And(
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
					Expect(getMetricsFromPod(c, getPodWithLabel(c, "app=olm-operator"))).ToNot(And(
						ContainElement(LikeMetric(WithFamily("csv_abnormal"), WithName(failingCSV.Name))),
						ContainElement(LikeMetric(WithFamily("csv_succeeded"), WithName(failingCSV.Name))),
					))
				})
			})
		})

		When("a CSV is created", func() {
			var (
				cleanupCSV cleanupFunc
				csv        v1alpha1.ClusterServiceVersion
			)
			BeforeEach(func() {
				packageName := genName("csv-test-")
				packageStable := fmt.Sprintf("%s-stable", packageName)
				csv = newCSV(packageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, nil, nil)

				var err error
				_, err = createCSV(c, crc, csv, testNamespace, false, false)
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCSV(crc, csv.Name, testNamespace, csvSucceededChecker)
				Expect(err).ToNot(HaveOccurred())
			})
			AfterEach(func() {
				if cleanupCSV != nil {
					cleanupCSV()
				}
			})
			It("emits a CSV metrics", func() {
				Expect(getMetricsFromPod(c, getPodWithLabel(c, "app=olm-operator"))).To(
					ContainElement(LikeMetric(WithFamily("csv_succeeded"), WithName(csv.Name), WithValue(1))),
				)
			})
			When("the OLM pod restarts", func() {
				BeforeEach(func() {
					restartDeploymentWithLabel(c, "app=olm-operator")
				})
				It("CSV metric is preserved", func() {
					Eventually(func() []Metric {
						return getMetricsFromPod(c, getPodWithLabel(c, "app=olm-operator"))
					}).Should(ContainElement(LikeMetric(
						WithFamily("csv_succeeded"),
						WithName(csv.Name),
						WithValue(1),
					)))
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
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
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
						return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
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
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
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
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
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
						return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
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
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
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
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
				}).ShouldNot(ContainElement(LikeMetric(WithFamily("subscription_sync_total"), WithName("metric-subscription-for-delete"))))
			})
		})
	})

	Context("Metrics emitted by CatalogSources", func() {
		When("A valid CatalogSource object is created", func() {
			var (
				name    = "metrics-catsrc-valid"
				cleanup func()
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
				cs, cleanupAll := createInternalCatalogSource(c, crc, name, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV})
				// Note(tflannag): Dependending on how ginkgo orders these test specs, and how bloated the cluster we're running
				// this test case against, we risk creating and then immediately deleting the catalogsource before the catalog
				// operator can generate all the requisite resources (e.g. the ServiceAccount), which can leave the underlying
				// registry Pod in a terminating state until kubelet times out waiting for the generated ServiceAccount
				// resource to be present so it can mount it in the registry container.
				_, err := fetchCatalogSourceOnStatus(crc, cs.GetName(), cs.GetNamespace(), catalogSourceRegistryPodSynced)
				Expect(err).ShouldNot(HaveOccurred())

				var once sync.Once
				cleanup = func() {
					once.Do(cleanupAll)
				}
			})
			AfterEach(func() {
				cleanup()
			})
			It("emits metrics for the catalogSource", func() {
				Eventually(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
				}).Should(And(
					ContainElement(LikeMetric(
						WithFamily("catalog_source_count"),
						WithValueGreaterThan(0),
					)),
					ContainElement(LikeMetric(
						WithFamily("catalogsource_ready"),
						WithName(name),
						WithNamespace(testNamespace),
						WithValue(1),
					)),
				))
			})
			When("The CatalogSource object is deleted", func() {
				BeforeEach(func() {
					cleanup()
				})
				It("deletes the metrics for the CatalogSource", func() {
					Eventually(func() []Metric {
						return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
					}).Should(And(
						Not(ContainElement(LikeMetric(
							WithFamily("catalogsource_ready"),
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
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
				}).Should(And(
					ContainElement(LikeMetric(
						WithFamily("catalogsource_ready"),
						WithName(name),
						WithNamespace(testNamespace),
						WithValue(0),
					)),
				))
				Consistently(func() []Metric {
					return getMetricsFromPod(c, getPodWithLabel(c, "app=catalog-operator"))
				}, "3m").Should(And(
					ContainElement(LikeMetric(
						WithFamily("catalogsource_ready"),
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

func getDeploymentWithLabel(client operatorclient.ClientInterface, label string) *appsv1.Deployment {
	listOptions := metav1.ListOptions{LabelSelector: label}
	var deploymentList *appsv1.DeploymentList
	EventuallyWithOffset(1, func() (numDeps int, err error) {
		deploymentList, err = client.KubernetesInterface().AppsV1().Deployments(operatorNamespace).List(context.TODO(), listOptions)
		if deploymentList != nil {
			numDeps = len(deploymentList.Items)
		}

		return
	}).Should(Equal(1), "expected exactly one Deployment")

	return &deploymentList.Items[0]
}

func restartDeploymentWithLabel(client operatorclient.ClientInterface, l string) {
	d := getDeploymentWithLabel(client, l)
	z := int32(0)
	oldZ := *d.Spec.Replicas
	d.Spec.Replicas = &z
	_, err := client.KubernetesInterface().AppsV1().Deployments(operatorNamespace).Update(context.TODO(), d, metav1.UpdateOptions{})
	Expect(err).ToNot(HaveOccurred())

	EventuallyWithOffset(1, func() (replicas int32, err error) {
		deployment, err := client.KubernetesInterface().AppsV1().Deployments(operatorNamespace).Get(context.TODO(), d.Name, metav1.GetOptions{})
		if deployment != nil {
			replicas = deployment.Status.Replicas
		}
		return
	}).Should(Equal(int32(0)), "expected exactly 0 Deployments")

	updated := getDeploymentWithLabel(client, l)
	updated.Spec.Replicas = &oldZ
	_, err = client.KubernetesInterface().AppsV1().Deployments(operatorNamespace).Update(context.TODO(), updated, metav1.UpdateOptions{})
	Expect(err).ToNot(HaveOccurred())

	EventuallyWithOffset(1, func() (replicas int32, err error) {
		deployment, err := client.KubernetesInterface().AppsV1().Deployments(operatorNamespace).Get(context.TODO(), d.Name, metav1.GetOptions{})
		if deployment != nil {
			replicas = deployment.Status.Replicas
		}
		return
	}).Should(Equal(oldZ), "expected exactly 1 Deployment")
}

func extractMetricPortFromPod(pod *corev1.Pod) string {
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.Name == "metrics" {
				return strconv.Itoa(int(port.ContainerPort))
			}
		}
	}
	return "-1"
}

func getMetricsFromPod(client operatorclient.ClientInterface, pod *corev1.Pod) []Metric {
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
			Name(net.JoinSchemeNamePort(scheme, pod.GetName(), extractMetricPortFromPod(pod))).
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
