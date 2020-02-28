package e2e_ginkgo

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"
	e2e "k8s.io/kubernetes/test/e2e/framework"

	g "github.com/onsi/ginkgo"
	o "github.com/onsi/gomega"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/e2e_ginkgo/util"
)

// The test validates pod metrics that are generated from a failed csv
var _ = g.Describe("Pod Metrics", func() {
	var (
		c          operatorclient.ClientInterface
		crc        versioned.Interface
		failingCSV v1alpha1.ClusterServiceVersion
	)

	g.It("should be generated when", func() {
		c = util.NewKubeClient(*util.KubeConfigPath)
		crc = util.NewCRClient(*util.KubeConfigPath)
		failingCSV = v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: util.GenName("failing-csv-test-"),
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				InstallStrategy: v1alpha1.NamedInstallStrategy{
					StrategyName: v1alpha1.InstallStrategyNameDeployment,
					StrategySpec: util.Strategy,
				},
			},
		}

		g.By("creating a failing CSV")
		cleanupCSV, err := util.CreateCSV(c, crc, failingCSV, testNamespace, false, false)
		o.Expect(err).NotTo(o.HaveOccurred())
		defer cleanupCSV()

		g.By("fetching the created CSV")
		_, err = util.FetchCSV(crc, failingCSV.Name, testNamespace, util.CsvFailedChecker)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Get metrics from pod and verify output")

		rawOutput, err := getMetricsFromPod(c, getOLMPodName(c), operatorNamespace, "8081")
		if err != nil {
			e2e.Failf("Metrics test failed: %v\n", err)
		}

		// Verify metrics have been emitted for package server csv
		o.Expect(rawOutput).To(o.ContainSubstring("csv_abnormal"))
		o.Expect(rawOutput).To(o.ContainSubstring("name=\"" + failingCSV.Name + "\""))
		o.Expect(rawOutput).To(o.ContainSubstring("phase=\"Failed\""))
		o.Expect(rawOutput).To(o.ContainSubstring("reason=\"UnsupportedOperatorGroup\""))
		o.Expect(rawOutput).To(o.ContainSubstring("version=\"0.0.0\""))
		o.Expect(rawOutput).To(o.ContainSubstring("csv_succeeded"))

	})
})

// Function that returns a OLM's pod name
func getOLMPodName(client operatorclient.ClientInterface) string {
	listOptions := metav1.ListOptions{LabelSelector: "app=olm-operator"}
	podList, err := client.KubernetesInterface().CoreV1().Pods(operatorNamespace).List(listOptions)
	if err != nil {
		e2e.Failf("Listing pods failed: %v\n", err)
	}
	if len(podList.Items) != 1 {
		e2e.Failf("Expected 1 olm-operator pod, got %v", len(podList.Items))
	}

	podName := podList.Items[0].GetName()
	e2e.Logf("Looking at pod %s in namespace %s", podName, operatorNamespace)
	return podName

}

// This function gets metrics for a given pod name
func getMetricsFromPod(client operatorclient.ClientInterface, podName string, namespace string, port string) (string, error) {
	olmPod, err := client.KubernetesInterface().CoreV1().Pods(namespace).Get(podName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if len(olmPod.Spec.Containers) != 1 {
		e2e.Failf("Expected only 1 container in olm-operator pod, got %v", len(olmPod.Spec.Containers))
	}

	var foundCert bool
	var foundKey bool
	// assuming -tls-cert and -tls-key aren't used anywhere else as a parameter value
	for _, param := range olmPod.Spec.Containers[0].Args {
		if param == "-tls-cert" {
			foundCert = true
		} else if param == "-tls-key" {
			foundKey = true
		}
	}

	var scheme string
	if foundCert && foundKey {
		scheme = "https"
	} else {
		scheme = "http"
	}
	e2e.Logf("Retrieving metrics using scheme %v", scheme)

	rawOutput, err := client.KubernetesInterface().CoreV1().RESTClient().Get().
		Namespace(namespace).
		Resource("pods").
		SubResource("proxy").
		Name(net.JoinSchemeNamePort(scheme, podName, port)).
		Suffix("metrics").
		Do().Raw()
	if err != nil {
		return "", err
	}
	return string(rawOutput), nil
}
