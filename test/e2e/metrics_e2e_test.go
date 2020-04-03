// +build !bare

package e2e

import (
	"context"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"

	. "github.com/onsi/ginkgo"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
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
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		_, err = fetchCSV(GinkgoT(), crc, failingCSV.Name, testNamespace, csvFailedChecker)
		require.NoError(GinkgoT(), err)

		rawOutput, err := getMetricsFromPod(GinkgoT(), c, getOLMPodName(GinkgoT(), c), operatorNamespace, "8081")
		if err != nil {
			GinkgoT().Fatalf("Metrics test failed: %v\n", err)
		}

		// Verify metrics have been emitted for packageserver csv
		require.Contains(GinkgoT(), rawOutput, "csv_abnormal")
		require.Contains(GinkgoT(), rawOutput, "name=\""+failingCSV.Name+"\"")
		require.Contains(GinkgoT(), rawOutput, "phase=\"Failed\"")
		require.Contains(GinkgoT(), rawOutput, "reason=\"UnsupportedOperatorGroup\"")
		require.Contains(GinkgoT(), rawOutput, "version=\"0.0.0\"")

		require.Contains(GinkgoT(), rawOutput, "csv_succeeded")
		log.Info(rawOutput)
	})
})

func getOLMPodName(t GinkgoTInterface, client operatorclient.ClientInterface) string {
	listOptions := metav1.ListOptions{LabelSelector: "app=olm-operator"}
	podList, err := client.KubernetesInterface().CoreV1().Pods(operatorNamespace).List(context.TODO(), listOptions)
	if err != nil {
		log.Infof("Error %v\n", err)
		t.Fatalf("Listing pods failed: %v\n", err)
	}
	if len(podList.Items) != 1 {
		t.Fatalf("Expected 1 olm-operator pod, got %v", len(podList.Items))
	}

	podName := podList.Items[0].GetName()
	log.Infof("Looking at pod %v in namespace %v", podName, operatorNamespace)
	return podName

}

func getMetricsFromPod(t GinkgoTInterface, client operatorclient.ClientInterface, podName string, namespace string, port string) (string, error) {
	olmPod, err := client.KubernetesInterface().CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if len(olmPod.Spec.Containers) != 1 {
		t.Fatalf("Expected only 1 container in olm-operator pod, got %v", len(olmPod.Spec.Containers))
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
	log.Infof("Retrieving metrics using scheme %v\n", scheme)

	rawOutput, err := client.KubernetesInterface().CoreV1().RESTClient().Get().
		Namespace(namespace).
		Resource("pods").
		SubResource("proxy").
		Name(net.JoinSchemeNamePort(scheme, podName, port)).
		Suffix("metrics").
		Do(context.TODO()).Raw()
	if err != nil {
		return "", err
	}
	return string(rawOutput), nil
}
