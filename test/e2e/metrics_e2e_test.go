// +build !bare

package e2e

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"
	"regexp"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Metrics are generated for OLM pod", func() {

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
				Expect(getMetricsFromPod(c, getOLMPod(c), "8081")).To(And(
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
					Expect(getMetricsFromPod(c, getOLMPod(c), "8081")).ToNot(And(
						ContainSubstring("csv_abnormal{name=\"%s\"", failingCSV.Name),
						ContainSubstring("csv_succeeded{name=\"%s\"", failingCSV.Name),
					))
				})
			})
		})
	})
})

func getOLMPod(client operatorclient.ClientInterface) *corev1.Pod {
	listOptions := metav1.ListOptions{LabelSelector: "app=olm-operator"}
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
