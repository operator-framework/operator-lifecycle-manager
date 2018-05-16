package client

import (
	"fmt"
	"net/http"
	"time"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	optypes "github.com/coreos-inc/tectonic-operators/operator-client/pkg/types"
)

const (
	// Timeouts
	podEvictionTimeout = 15 * time.Second

	// Polling durations
	cordonPollDuration       = 20 * time.Millisecond
	evictPollDuration        = time.Second
	atomicNodeUpdateDuration = time.Second
)

// ListNodes returns a list of Nodes.
func (c *Client) ListNodes(lo metav1.ListOptions) (*v1.NodeList, error) {
	glog.V(4).Info("[GET Node list]")
	return c.CoreV1().Nodes().List(lo)
}

// GetNode will return the Node object specified by the given name.
func (c *Client) GetNode(name string) (*v1.Node, error) {
	glog.V(4).Infof("[GET Node]: %s", name)
	return c.CoreV1().Nodes().Get(name, metav1.GetOptions{})
}

// UpdateNode will update the Node object given.
func (c *Client) UpdateNode(node *v1.Node) (*v1.Node, error) {
	glog.V(4).Infof("[UPDATE Node]: %s", node.GetName())
	oldNode, err := c.CoreV1().Nodes().Get(node.GetName(), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error getting existing Node %s for patch: %v", node.GetName(), err)
	}
	patchBytes, err := createPatch(oldNode, node)
	if err != nil {
		return nil, fmt.Errorf("error creating patch: %v", err)
	}
	return c.CoreV1().Nodes().Patch(node.GetName(), types.StrategicMergePatchType, patchBytes)
}

// AtomicUpdateNode will continue to apply the update function to the
// Node object referenced by the given name until the update
// succeeds without conflict or returns an error.
func (c *Client) AtomicUpdateNode(name string, f optypes.NodeModifier) (*v1.Node, error) {
	var n *v1.Node
	err := wait.PollInfinite(atomicNodeUpdateDuration, func() (bool, error) {
		var err error
		n, err = c.GetNode(name)
		if err != nil {
			return false, err
		}
		if err = f(n); err != nil {
			return false, err
		}
		n, err = c.UpdateNode(n)
		if err != nil {
			if apierrors.IsConflict(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return n, nil
}

// CordonNode will mark the Node 'n' as unschedulable.
func (c *Client) CordonNode(n *v1.Node) (*v1.Node, error) {
	glog.V(4).Infof("[CORDON Node]: %s", n.GetName())
	return cordonOrUncordon(c, n, true)
}

// UnCordonNode will mark the Node 'n' as schedulable.
func (c *Client) UnCordonNode(n *v1.Node) (*v1.Node, error) {
	glog.V(4).Infof("[UNCORDON Node]: %s", n.GetName())
	return cordonOrUncordon(c, n, false)
}

func cordonOrUncordon(c *Client, n *v1.Node, cordon bool) (*v1.Node, error) {
	var nn *v1.Node
	err := wait.PollInfinite(cordonPollDuration, func() (bool, error) {
		n.Spec.Unschedulable = cordon
		var err error
		nn, err = c.UpdateNode(n)
		if err != nil {
			if apierrors.IsConflict(err) {
				n, err = c.GetNode(n.GetName())
				if err != nil {
					return false, err
				}
				return false, nil
			}
			return false, fmt.Errorf("error changing schedulable status of Node %s to %v: %v", n.Name, false, err)
		}
		return true, nil
	})
	return nn, err
}

// DrainNode will drain all Pods from the given Node, with the exception of
// DaemonSet managed Pods.
func (c *Client) DrainNode(n *v1.Node) error {
	glog.V(4).Infof("[DRAIN Node]: %s", n.GetName())
	pods, err := GetPodsForEviction(c, n.GetName())
	if err != nil {
		return fmt.Errorf("error getting pods for deletion when attempting to drain Node %s: %v", n.GetName(), err)
	}
	for _, p := range pods {
		if err := evictPod(c, p, n); err != nil {
			return err
		}
	}
	return nil
}

// OptimisticDrainNode will attempt to drain all Pods from the given Node, with the
// exception of DaemonSet managed Pods. If it cannot evict a Pod due to the
// disruption budget, it will skip that Pod and move on.
func (c *Client) OptimisticDrainNode(n *v1.Node) error {
	glog.V(4).Infof("[OPTIMISTIC DRAIN Node]: %s", n.GetName())
	pods, err := GetPodsForEviction(c, n.GetName())
	if err != nil {
		return fmt.Errorf("error getting pods for deletion when attempting to drain Node %s: %v", n.GetName(), err)
	}
	for _, p := range pods {
		if err := tryEvictPod(c, p, n); err != nil {
			return err
		}
	}
	return nil
}

func evictPod(c *Client, p v1.Pod, n *v1.Node) error {
	return evict(c, p, n, false)
}

func tryEvictPod(c *Client, p v1.Pod, n *v1.Node) error {
	return evict(c, p, n, true)
}

func evict(c *Client, p v1.Pod, n *v1.Node, optimistic bool) error {
	err := wait.Poll(evictPollDuration, podEvictionTimeout, func() (bool, error) {
		res := doEvictionRequest(c, p)
		if disruptionBudgetHit(res) {
			glog.Infof("unable to evict pod %s due to disruption budget", p.Name)
			return optimistic, nil
		}
		if res.Error() != nil {
			return false, fmt.Errorf("error evicting Pod %s for drain on Node %s: %v", p.Name, n.Name, res.Error())
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("error trying to evict Pod %s: %v", p.Name, err)
	}
	return nil
}

// Check to see if the disruption budget has been hit.
// We accomplish this by examining the HTTP status code returned
// from the request. The documentation states that '429 Too Many Requests'
// will be returned from the API server in the event that an eviction request
// fails due to a disruption budget.
//
// https://kubernetes.io/docs//tasks/administer-cluster/safely-drain-node/#the-eviction-api
func disruptionBudgetHit(res rest.Result) bool {
	var status int
	res.StatusCode(&status)
	return status == http.StatusTooManyRequests
}

func doEvictionRequest(c *Client, p v1.Pod) rest.Result {
	eviction := &policyv1beta1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: p.GetNamespace(),
			Name:      p.GetName(),
		},
	}
	return c.PolicyV1beta1().
		RESTClient().
		Post().
		AbsPath("/api/v1").
		Namespace(eviction.GetNamespace()).
		Resource("pods").
		Name(eviction.GetName()).
		SubResource("eviction").
		Body(eviction).
		Do()
}

// GetPodsForEviction will return a list of pods that should be evicted from the Node
// during a drain. This list returns all pods running on a system except for mirror
// pods and DaemonSets.
func GetPodsForEviction(c Interface, node string) (pods []v1.Pod, err error) {
	pi := c.KubernetesInterface().CoreV1().Pods(metav1.NamespaceAll)
	podList, err := pi.List(metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node}).String(),
	})
	if err != nil {
		return pods, err
	}

	for _, pod := range podList.Items {
		// skip mirror pods
		if _, ok := pod.Annotations[v1.MirrorPodAnnotationKey]; ok {
			continue
		}

		or := metav1.GetControllerOf(&pod)
		if or != nil && or.Kind == "DaemonSet" {
			continue
		}

		pods = append(pods, pod)
	}

	return pods, nil
}
