// +build !bare

package e2e

import (
	"fmt"
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestMetrics tests the metrics endpoint of the OLM pod.
func TestMetricsEndpoint(t *testing.T) {
	c := newKubeClient(t)

	listOptions := metav1.ListOptions{LabelSelector: "app=olm-operator"}
	podList, err := c.KubernetesInterface().CoreV1().Pods(testNamespace).List(listOptions)
	if err != nil {
		log.Infof("Error %v\n", err)
		t.Fatalf("Listing pods failed: %v\n", err)
	}
	if len(podList.Items) > 1 {
		t.Fatalf("Expected only 1 olm-operator pod, got %v", len(podList.Items))
	}

	podName := podList.Items[0].GetName()

	rawOutput, err := getMetricsFromPod(t, c, podName, testNamespace, 8080)
	if err != nil {
		t.Fatalf("Metrics test failed: %v\n", err)
	}

	log.Debugf("Metrics:\n%v", rawOutput)
}

func getMetricsFromPod(t *testing.T, client operatorclient.ClientInterface, podName string, namespace string, port int) (string, error) {
	rawOutput, err := client.KubernetesInterface().CoreV1().RESTClient().Get().
		Namespace(namespace).
		Resource("pods").
		SubResource("proxy").
		Name(fmt.Sprintf("%v:%v", podName, port)).
		Suffix("metrics").
		Do().Raw()
	if err != nil {
		return "", err
	}
	return string(rawOutput), nil
}
