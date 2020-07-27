package main

import (
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"context"
)

// podCheckFunc describes a function that returns true if the given Pod meets some criteria; false otherwise.
type podCheckFunc func(pod *corev1.Pod) bool

func awaitPod(client operatorclient.ClientInterface, namespace, podName string, checkPod podCheckFunc) (*corev1.Pod, error) {
	var pod *corev1.Pod
	err := wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		p, err := client.KubernetesInterface().CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		pod = p
		return checkPod(pod), nil
	})
	if err != nil {
		return nil, err
	}
	return pod, nil
}

func Local(client operatorclient.ClientInterface) (bool, error) {
	const ClusterVersionGroup = "config.openshift.io"
	const ClusterVersionVersion = "v1"
	const ClusterVersionKind = "ClusterVersion"
	gv := metav1.GroupVersion{Group: ClusterVersionGroup, Version: ClusterVersionVersion}.String()

	groups, err := client.KubernetesInterface().Discovery().ServerResourcesForGroupVersion(gv)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return true, fmt.Errorf("checking if cluster is local: checking server groups: %s", err)
	}

	for _, group := range groups.APIResources {
		if group.Kind == ClusterVersionKind {
			return false, nil
		}
	}

	return true, nil
}

// podReady returns true if the given Pod has a ready condition with ConditionStatus "True"; false otherwise.
func podReady(pod *corev1.Pod) bool {
	var status corev1.ConditionStatus
	for _, condition := range pod.Status.Conditions {
		if condition.Type != corev1.PodReady {
			// Ignore all condition other than PodReady
			continue
		}

		// Found PodReady condition
		status = condition.Status
		break
	}

	return status == corev1.ConditionTrue
}