// +build !bare

package e2e

import (
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

// TestMetrics tests the metrics endpoint of the OLM pod.
func TestMetricsEndpoint(t *testing.T) {
	c := newKubeClient(t)

	listOptions := metav1.ListOptions{LabelSelector: "app=olm-operator"}
	podList, err := c.KubernetesInterface().CoreV1().Pods(operatorNamespace).List(listOptions)
	if err != nil {
		log.Infof("Error %v\n", err)
		t.Fatalf("Listing pods failed: %v\n", err)
	}
	if len(podList.Items) > 1 {
		t.Fatalf("Expected only 1 olm-operator pod, got %v", len(podList.Items))
	}

	podName := podList.Items[0].GetName()
	log.Infof("Looking at pod %v in namespace %v", podName, operatorNamespace)

	rawOutput, err := getMetricsFromPod(t, c, podName, operatorNamespace, "8081")
	if err != nil {
		t.Fatalf("Metrics test failed: %v\n", err)
	}

	// Verify metrics have been emitted for packageserver csv
	require.Contains(t, rawOutput, "csv_sync_total counter")
	require.Contains(t, rawOutput, "phase=\"Succeeded\"")
	require.Contains(t, rawOutput, "packageserver")
}

func getMetricsFromPod(t *testing.T, client operatorclient.ClientInterface, podName string, namespace string, port string) (string, error) {
	olmPod, err := client.KubernetesInterface().CoreV1().Pods(namespace).Get(podName, metav1.GetOptions{})
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
		Do().Raw()
	if err != nil {
		return "", err
	}
	return string(rawOutput), nil
}
