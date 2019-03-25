package e2e

import (
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

// TestOwnerReferenceGCBehavior runs a simple check on OwnerReference behavior to ensure
// a resource with multiple OwnerReferences will not be garbage collected when one of its
// owners has been deleted.
// Test Case:
//				CSV-A     CSV-B                        CSV-B
//				   \      /      --Delete CSV-A-->       |
//				   ConfigMap						 ConfigMap
func TestOwnerReferenceGCBehavior(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	ownerA := newCSV("ownera", testNamespace, "", *semver.New("0.0.0"), nil, nil, newNginxInstallStrategy("dep-", nil, nil))
	ownerB := newCSV("ownerb", testNamespace, "", *semver.New("0.0.0"), nil, nil, newNginxInstallStrategy("dep-", nil, nil))

	// create all owners
	c := newKubeClient(t)
	crc := newCRClient(t)

	fetchedA, err := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(&ownerA)
	require.NoError(t, err)
	fetchedB, err := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(&ownerB)
	require.NoError(t, err)

	dependent := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dependent",
		},
		Data: map[string]string{},
	}

	// add owners
	ownerutil.AddOwner(dependent, fetchedA, true, false)
	ownerutil.AddOwner(dependent, fetchedB, true, false)

	// create dependent
	_, err = c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Create(dependent)
	require.NoError(t, err, "dependent could not be created")

	// delete ownerA in the foreground (to ensure any "blocking" dependents are deleted before ownerA)
	propagation := metav1.DeletionPropagation("Foreground")
	options := metav1.DeleteOptions{PropagationPolicy: &propagation}
	err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(fetchedA.GetName(), &options)
	require.NoError(t, err)

	// wait for deletion of ownerA
	waitForDelete(func() error {
		_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(ownerA.GetName(), metav1.GetOptions{})
		return err
	})

	// check for dependent (should still exist since it still has one owner present)
	_, err = c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(dependent.GetName(), metav1.GetOptions{})
	require.NoError(t, err, "dependent deleted after one owner was deleted")
	t.Log("dependent still exists after one owner was deleted")

	// delete ownerB in the foreground (to ensure any "blocking" dependents are deleted before ownerB)
	err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(fetchedB.GetName(), &options)
	require.NoError(t, err)

	// wait for deletion of ownerB
	waitForDelete(func() error {
		_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(ownerB.GetName(), metav1.GetOptions{})
		return err
	})

	// check for dependent (should be deleted since last blocking owner was deleted)
	_, err = c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(dependent.GetName(), metav1.GetOptions{})
	require.Error(t, err)
	require.True(t, k8serrors.IsNotFound(err))
	t.Log("dependent successfully garbage collected after both owners were deleted")
}
