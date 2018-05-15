package client

import (
	"time"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ListPodsWithLabels will return a list of Pods in the given namespace
// filtered by the given label selector.
func (c *Client) ListPodsWithLabels(namespace string, labels labels.Set) (*v1.PodList, error) {
	glog.V(4).Infof("[LIST Pods] in: %s, labels: %s", namespace, labels)

	opts := metav1.ListOptions{LabelSelector: labels.String()}
	return c.CoreV1().Pods(namespace).List(opts)
}

// GetPod deletes the Pod with the given namespace and name.
func (c *Client) GetPod(namespace, name string) (*v1.Pod, error) {
	glog.V(4).Infof("[GET Pod]: %s:%s", namespace, name)
	return c.CoreV1().Pods(namespace).Get(name, metav1.GetOptions{})
}

// DeletePod deletes the Pod with the given namespace and name.
func (c *Client) DeletePod(namespace, name string) error {
	glog.V(4).Infof("[DELETE Pod]: %s:%s", namespace, name)
	return c.CoreV1().Pods(namespace).Delete(name, nil)
}

// IsPodAvailable returns true if a pod is available; false otherwise.
// Precondition for an available pod is that it must be ready. On top
// of that, there are two cases when a pod can be considered available:
// 1. minReadySeconds == 0, or
// 2. LastTransitionTime (is set) + minReadySeconds < current time
func IsPodAvailable(pod *v1.Pod, minReadySeconds int32, now metav1.Time) bool {
	if !IsPodReady(pod) {
		return false
	}

	c := GetPodReadyCondition(pod.Status)
	minReadySecondsDuration := time.Duration(minReadySeconds) * time.Second
	if minReadySeconds == 0 || !c.LastTransitionTime.IsZero() && c.LastTransitionTime.Add(minReadySecondsDuration).Before(now.Time) {
		return true
	}
	return false
}

// IsPodReady returns true if a pod is ready; false otherwise.
func IsPodReady(pod *v1.Pod) bool {
	return IsPodReadyConditionTrue(pod.Status)
}

// IsPodReadyConditionTrue returns true if a pod is ready; false otherwise.
func IsPodReadyConditionTrue(status v1.PodStatus) bool {
	condition := GetPodReadyCondition(status)
	return condition != nil && condition.Status == v1.ConditionTrue
}

// GetPodReadyCondition extracts the pod ready condition from the given status and returns that.
// Returns nil if the condition is not present.
func GetPodReadyCondition(status v1.PodStatus) *v1.PodCondition {
	_, condition := GetPodCondition(&status, v1.PodReady)
	return condition
}

// GetPodCondition extracts the provided condition from the given status and returns that.
// Returns nil and -1 if the condition is not present, and the index of the located condition.
func GetPodCondition(status *v1.PodStatus, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if status == nil {
		return -1, nil
	}
	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return i, &status.Conditions[i]
		}
	}
	return -1, nil
}
