// +build !bare

package e2e

import (
	"context"
	"fmt"
	"regexp"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

var _ = Describe("Metrics", func() {
	It("endpoint", func() {

		// TestMetrics tests the metrics endpoint of the OLM pod.

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

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
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, failingCSV.Name, testNamespace, csvFailedChecker)
		Expect(err).ToNot(HaveOccurred())

		// Verify metrics have been emitted for packageserver csv
		Expect(getMetricsFromPod(c, getOLMPod(c), "8081")).To(And(
			ContainSubstring("csv_abnormal"),
			ContainSubstring(fmt.Sprintf("name=\"%s\"", failingCSV.Name)),
			ContainSubstring("phase=\"Failed\""),
			ContainSubstring("reason=\"UnsupportedOperatorGroup\""),
			ContainSubstring("version=\"0.0.0\""),
			ContainSubstring("csv_succeeded"),
		))
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
