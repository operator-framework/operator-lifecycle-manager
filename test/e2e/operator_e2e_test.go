package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	operatorsv2alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v2alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
)

// TestOperatorComponentSelection ensures that an Operator resource can select its components by label and surface them correctly in its status.
//
// Steps:
// 1. Create an Operator resource, o
// 2. Ensure o's status eventually contains its component label selector
// 3. Create namespaces ns-a and ns-b
// 4. Label ns-a with o's component label
// 5. Ensure o's status.components.refs field eventually contains a reference to ns-a
// 6. Create ServiceAccounts sa-a and sa-b in namespaces ns-a and ns-b respectively
// 7. Label sa-a and sa-b with o's component label
// 8. Ensure o's status.components.refs field eventually contains references to sa-a and sa-b
// 9. Remove the component label from sa-b
// 10. Ensure the reference to sa-b is eventually removed from o's status.components.refs field
// 11. Delete ns-b
// 12. Ensure the reference to ns-b is eventually removed from o's status.components.refs field
func TestOperatorComponentSelection(t *testing.T) {
	// Toggle v2alpha1 feature-gate for this test
	c := newKubeClient(t)
	require.NoError(t, toggleCVO(t, c))
	require.NoError(t, togglev2alpha1(t, c))
	defer func() {
		require.NoError(t, togglev2alpha1(t, c))
		require.NoError(t, toggleCVO(t, c))
	}()

	// Create an operator resource, o
	crc := newCRClient(t)
	o := &operatorsv2alpha1.Operator{}
	o.SetName(genName("o-"))
	o, err := crc.OperatorsV2alpha1().Operators().Create(o)
	require.NoError(t, err)
	deleteOpts := &metav1.DeleteOptions{}
	defer func() {
		require.NoError(t, crc.OperatorsV2alpha1().Operators().Delete(o.GetName(), deleteOpts))
	}()

	// Ensure o's status eventually contains its component label selector
	w, err := crc.OperatorsV2alpha1().Operators().Watch(metav1.ListOptions{})
	require.NoError(t, err)

	deadline, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	expectedKey := "operators.coreos.com/" + o.GetName()
	awaitPredicates(deadline, t, w, operatorPredicate(func(op *operatorsv2alpha1.Operator) bool {
		if op.Status.Components == nil || op.Status.Components.LabelSelector == nil {
			return false
		}

		for _, requirement := range op.Status.Components.LabelSelector.MatchExpressions {
			if requirement.Key == expectedKey && requirement.Operator == metav1.LabelSelectorOpExists {
				return true
			}
		}

		return false
	}))
	w.Stop()

	// Create namespaces ns-a and ns-b
	nsA := &corev1.Namespace{}
	nsA.SetName(genName("ns-a-"))
	nsB := &corev1.Namespace{}
	nsB.SetName(genName("ns-b-"))

	for _, ns := range []*corev1.Namespace{nsA, nsB} {
		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(ns)
		require.NoError(t, err)
		defer func(name string) {
			c.KubernetesInterface().CoreV1().Namespaces().Delete(name, deleteOpts)
		}(ns.GetName())
	}

	// Label ns-a with o's component label
	nsA.SetLabels(map[string]string{expectedKey: ""})
	_, err = c.KubernetesInterface().CoreV1().Namespaces().Update(nsA)
	require.NoError(t, err)

	// Ensure o's status.components.refs field eventually contains a reference to ns-a
	checkPresence(t, crc, nsA.GetName())

	// Create ServiceAccounts sa-a and sa-b in namespaces ns-a and ns-b respectively
	saA := &corev1.ServiceAccount{}
	saA.SetName(genName("sa-a-"))
	saA.SetNamespace(nsA.Name)
	saB := &corev1.ServiceAccount{}
	saB.SetName(genName("sa-b-"))
	saB.SetNamespace(nsB.Name)

	for _, sa := range []*corev1.ServiceAccount{saA, saB} {
		_, err := c.KubernetesInterface().CoreV1().ServiceAccounts(sa.GetNamespace()).Create(sa)
		require.NoError(t, err)
		defer func(namespace, name string) {
			c.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Delete(name, deleteOpts)
		}(sa.GetNamespace(), sa.GetName())
	}

	// Label sa-a and sa-b with o's component label
	saA.SetLabels(map[string]string{expectedKey: ""})
	_, err = c.KubernetesInterface().CoreV1().ServiceAccounts(saA.GetNamespace()).Update(saA)
	require.NoError(t, err)
	saB.SetLabels(map[string]string{expectedKey: ""})
	_, err = c.KubernetesInterface().CoreV1().ServiceAccounts(saB.GetNamespace()).Update(saB)
	require.NoError(t, err)

	// Ensure o's status.components.refs field eventually contains references to sa-a and sa-b
	checkPresence(t, crc, saA.GetName())
	checkPresence(t, crc, saB.GetName())

	// Remove the component label from sa-b
	saB.SetLabels(nil)
	_, err = c.KubernetesInterface().CoreV1().ServiceAccounts(saB.GetNamespace()).Update(saB)
	require.NoError(t, err)

	// Ensure the reference to sa-b is eventually removed from o's status.components.refs field
	checkAbsence(t, crc, saB.GetName())

	// Delete ns-b
	require.NoError(t, c.KubernetesInterface().CoreV1().Namespaces().Delete(nsB.GetName(), deleteOpts))

	// Ensure the reference to ns-b is eventually removed from o's status.components.refs field
	checkAbsence(t, crc, nsB.GetName())
}

func checkPresence(t *testing.T, crc versioned.Interface, refName string) {
	w, err := crc.OperatorsV2alpha1().Operators().Watch(metav1.ListOptions{})
	require.NoError(t, err)
	defer w.Stop()

	deadline, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	awaitPredicates(deadline, t, w, operatorPredicate(func(op *operatorsv2alpha1.Operator) bool {
		if op.Status.Components == nil || op.Status.Components.Refs == nil {
			return false
		}

		for _, ref := range op.Status.Components.Refs {
			if ref.Name == refName {
				return true
			}
		}

		return false
	}))
}

func checkAbsence(t *testing.T, crc versioned.Interface, refName string) {
	w, err := crc.OperatorsV2alpha1().Operators().Watch(metav1.ListOptions{})
	require.NoError(t, err)
	defer w.Stop()

	deadline, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	awaitPredicates(deadline, t, w, operatorPredicate(func(op *operatorsv2alpha1.Operator) bool {
		if op.Status.Components == nil || op.Status.Components.Refs == nil {
			return false
		}

		for _, ref := range op.Status.Components.Refs {
			if ref.Name == refName {
				return false
			}
		}

		return true
	}))
}

func operatorPredicate(fn func(*operatorsv2alpha1.Operator) bool) predicateFunc {
	return func(t *testing.T, event watch.Event) bool {
		o, ok := event.Object.(*operatorsv2alpha1.Operator)
		if !ok {
			panic(fmt.Sprintf("unexpected event object type %T in deployment", event.Object))
		}

		return fn(o)
	}
}
