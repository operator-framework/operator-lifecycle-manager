package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"

	"github.com/coreos/go-semver/semver"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func DeploymentComplete(deployment *appsv1.Deployment, newStatus *appsv1.DeploymentStatus) bool {
	return newStatus.UpdatedReplicas == *(deployment.Spec.Replicas) &&
		newStatus.Replicas == *(deployment.Spec.Replicas) &&
		newStatus.AvailableReplicas == *(deployment.Spec.Replicas) &&
		newStatus.ObservedGeneration >= deployment.Generation
}

// Currently this function only modifies the watchedNamespace in the container command
func patchOlmDeployment(t *testing.T, c operatorclient.ClientInterface, newNamespace string) []string {
	runningDeploy, err := c.GetDeployment(testNamespace, "olm-operator")
	require.NoError(t, err)

	command := runningDeploy.Spec.Template.Spec.Containers[0].Command
	re, err := regexp.Compile(`-watchedNamespaces\W(\S+)`)
	require.NoError(t, err)
	newCommand := re.ReplaceAllString(strings.Join(command, " "), "$0"+","+newNamespace)
	log.Debugf("original=%#v newCommand=%#v", command, newCommand)
	finalNewCommand := strings.Split(newCommand, " ")
	runningDeploy.Spec.Template.Spec.Containers[0].Command = make([]string, len(finalNewCommand))
	copy(runningDeploy.Spec.Template.Spec.Containers[0].Command, finalNewCommand)

	newDeployment, updated, err := c.UpdateDeployment(runningDeploy)
	if err != nil || updated == false {
		t.Fatalf("Deployment update failed: (updated %v) %v\n", updated, err)
	}
	require.NoError(t, err)

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedDeployment, err := c.GetDeployment(newDeployment.Namespace, newDeployment.Name)
		if err != nil {
			return false, err
		}
		if DeploymentComplete(newDeployment, &fetchedDeployment.Status) {
			return true, nil
		}
		return false, nil
	})

	require.NoError(t, err)
	return command
}

func checkOperatorGroupAnnotations(obj metav1.Object, op *v1alpha2.OperatorGroup, targetNamespaces string) error {
	if annotation, ok := obj.GetAnnotations()["olm.targetNamespaces"]; !ok || annotation != targetNamespaces {
		return fmt.Errorf("missing targetNamespaces annotation on %v", obj.GetName())
	}
	if annotation, ok := obj.GetAnnotations()["olm.operatorNamespace"]; !ok || annotation != op.GetNamespace() {
		return fmt.Errorf("missing operatorNamespace on %v", obj.GetName())
	}
	if annotation, ok := obj.GetAnnotations()["olm.operatorGroup"]; !ok || annotation != op.GetName() {
		return fmt.Errorf("missing operatorGroup annotation on %v", obj.GetName())
	}

	return nil
}

func TestOperatorGroup(t *testing.T) {
	// Create namespace with specific label
	// Create CSV in operator namespace
	// Create operator group that watches namespace and uses specific label
	// Verify operator group status contains correct status
	// Verify csv in target namespace exists, has copied status, has annotations
	// Verify deployments have correct namespace annotation
	// (Verify that the operator can operate in the target namespace)

	log.SetLevel(log.DebugLevel)
	c := newKubeClient(t)
	crc := newCRClient(t)
	csvName := "another-csv" // must be lowercase for DNS-1123 validation

	matchingLabel := map[string]string{"matchLabel": testNamespace}
	otherNamespaceName := testNamespace + "-namespace-two"

	otherNamespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   otherNamespaceName,
			Labels: matchingLabel,
		},
	}
	createdOtherNamespace, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&otherNamespace)
	require.NoError(t, err)

	oldCommand := patchOlmDeployment(t, c, otherNamespaceName)

	log.Debug("Creating CSV")
	aCSV := newCSV(csvName, testNamespace, "", *semver.New("0.0.0"), nil, nil, newNginxInstallStrategy("operator-deployment", nil, nil))
	createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(&aCSV)
	require.NoError(t, err)

	log.Debug("Creating operator group")
	operatorGroup := v1alpha2.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-operator-group",
			Namespace: testNamespace,
		},
	}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Create(&operatorGroup)
	require.NoError(t, err)
	expectedOperatorGroupStatus := v1alpha2.OperatorGroupStatus{
		Namespaces: []*corev1.Namespace{createdOtherNamespace},
	}

	// instead of setting the label selector initially, do it here to force immediate reconcile
	fetchedOpGroup, err := crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Get(operatorGroup.GetName(), metav1.GetOptions{})
	fetchedOpGroup.Spec = v1alpha2.OperatorGroupSpec{
		Selector: metav1.LabelSelector{
			MatchLabels: matchingLabel,
		},
	}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Update(fetchedOpGroup)
	require.NoError(t, err)

	log.Debug("Waiting on operator group to have correct status")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, fetchErr := crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Get(operatorGroup.Name, metav1.GetOptions{})
		if fetchErr != nil {
			fmt.Println(fetchErr)
			return false, fetchErr
		}
		if len(fetched.Status.Namespaces) > 0 {
			require.Equal(t, expectedOperatorGroupStatus.Namespaces[0].Name, fetched.Status.Namespaces[0].Name)
			return true, nil
		}
		return false, nil
	})

	log.Debug("Waiting for operator namespace csv to have annotations")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			fmt.Println(fetchErr)
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, otherNamespaceName) == nil {
			return true, nil
		}
		return false, nil
	})

	log.Debug("Waiting for target namespace csv to have annotations")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			fmt.Println(fetchErr)
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, otherNamespaceName) == nil {
			return true, nil
		}

		return false, nil
	})
	// since annotations are set along with status, no reason to poll for this check as done above
	log.Debug("Checking status on csv in target namespace")
	fetchedCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
	require.NoError(t, err)
	require.EqualValues(t, v1alpha1.CSVReasonCopied, fetchedCSV.Status.Reason)
	// also check name and spec
	require.Equal(t, createdCSV.Name, fetchedCSV.Name)
	require.Equal(t, createdCSV.Spec, fetchedCSV.Spec)

	log.Debug("Waiting on deployment to have correct annotation")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		createdDeployment, err := c.GetDeployment(testNamespace, "operator-deployment")
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		// TODO: verify operatorNamespace annotation, operatorGroup annotation
		if createdDeployment.Spec.Template.Annotations["olm.targetNamespaces"] == otherNamespaceName {
			return true, nil
		}
		return false, nil
	})

	// ensure deletion cleans up copied CSV
	log.Debug("Deleting CSV")
	err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(csvName, &metav1.DeleteOptions{})
	require.NoError(t, err)

	log.Debug("Waiting for orphaned CSV to be deleted")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
	require.NoError(t, err)

	// clean up
	// TODO: unpatch function
	runningDeploy, err := c.GetDeployment(testNamespace, "olm-operator")
	require.NoError(t, err)
	runningDeploy.Spec.Template.Spec.Containers[0].Command = oldCommand
	_, updated, err := c.UpdateDeployment(runningDeploy)
	if err != nil || updated == false {
		t.Fatalf("Deployment update failed: (updated %v) %v\n", updated, err)
	}
	require.NoError(t, err)

	err = c.KubernetesInterface().CoreV1().Namespaces().Delete(otherNamespaceName, &metav1.DeleteOptions{})
	require.NoError(t, err)
	err = crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Delete(operatorGroup.Name, &metav1.DeleteOptions{})
	require.NoError(t, err)
}
