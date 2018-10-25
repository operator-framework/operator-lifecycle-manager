package e2e

import (
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

func TestCreateOperatorGroupWithMatchingNamespace(t *testing.T) {
	// Create namespace with specific label
	// Create deployment in namespace
	// Create operator group that watches namespace and uses specific label
	// Verify operator group status contains correct status
	// Verify deployments have correct namespace annotation

	log.SetLevel(log.DebugLevel)
	c := newKubeClient(t)
	crc := newCRClient(t)

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

	var one = int32(1)
	deployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deployment",
			Namespace: otherNamespaceName,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: matchingLabel,
			},
			Replicas: &one,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: matchingLabel,
				},
				Spec: corev1.PodSpec{Containers: []corev1.Container{
					{
						Name:  genName("nginx"),
						Image: "nginx:1.7.9",
						Ports: []corev1.ContainerPort{{ContainerPort: 80}},
					},
				}},
			},
		},
	}

	createdDeployment, err := c.CreateDeployment(&deployment)
	require.NoError(t, err)

	operatorGroup := v1alpha2.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-operator-group",
			Namespace: testNamespace,
		},
		Spec: v1alpha2.OperatorGroupSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: matchingLabel,
			},
		},
	}
	createdOperatorGroup, err := crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Create(&operatorGroup)
	require.NoError(t, err)
	expectedOperatorGroupStatus := v1alpha2.OperatorGroupStatus{
		Namespaces: []*corev1.Namespace{createdOtherNamespace},
	}

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		createdOperatorGroup, err = crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Get(operatorGroup.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if len(createdOperatorGroup.Status.Namespaces) > 0 {
			require.Equal(t, expectedOperatorGroupStatus.Namespaces[0].Name, createdOperatorGroup.Status.Namespaces[0].Name)
			return true, nil
		}
		return false, nil
	})

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		createdDeployment, err = c.GetDeployment(otherNamespaceName, "deployment")
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if createdDeployment.Spec.Template.Annotations["olm.targetNamespaces"] == otherNamespaceName {
			return true, nil
		}
		return false, nil
	})

	// clean up
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

func TestCreateOperatorGroupCSVCopy(t *testing.T) {
	// create target namespace
	// create operator group in OLM namespace, which serves as operator namespace
	// create CSV in OLM namespace
	// verify CSV is copied to target namespace

	log.SetLevel(log.DebugLevel)
	c := newKubeClient(t)
	crc := newCRClient(t)
	targetNamespaceName := testNamespace + "-target"
	csvName := "acsv-that-is-unique" // must be lowercase for DNS-1123 validation
	matchingLabel := map[string]string{"matchLabel": testNamespace}

	operatorNamespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   targetNamespaceName,
			Labels: matchingLabel,
		},
	}
	_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&operatorNamespace)
	require.NoError(t, err)

	oldCommand := patchOlmDeployment(t, c, targetNamespaceName)

	operatorGroup := v1alpha2.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-operator-group",
			Namespace: testNamespace,
		},
		Spec: v1alpha2.OperatorGroupSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: matchingLabel,
			},
		},
	}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Create(&operatorGroup)
	require.NoError(t, err)

	aCSV := newCSV(csvName, testNamespace, "", *semver.New("0.0.0"), nil, nil, newNginxInstallStrategy("aspec", nil, nil))
	createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(&aCSV)
	require.NoError(t, err)

	var csvCopy *v1alpha1.ClusterServiceVersion
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		csvCopy, err = crc.OperatorsV1alpha1().ClusterServiceVersions(targetNamespaceName).Get(csvName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	require.Equal(t, createdCSV.Name, csvCopy.Name)
	require.Equal(t, createdCSV.Spec, csvCopy.Spec)

	// part 2 - ensure deletion cleans up copied CSV
	err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(csvName, &metav1.DeleteOptions{})
	require.NoError(t, err)

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		csvCopy, err = crc.OperatorsV1alpha1().ClusterServiceVersions(targetNamespaceName).Get(csvName, metav1.GetOptions{})
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
	runningDeploy, err := c.GetDeployment(testNamespace, "olm-operator")
	require.NoError(t, err)
	runningDeploy.Spec.Template.Spec.Containers[0].Command = oldCommand
	_, updated, err := c.UpdateDeployment(runningDeploy)
	if err != nil || updated == false {
		t.Fatalf("Deployment update failed: (updated %v) %v\n", updated, err)
	}
	require.NoError(t, err)

	err = c.KubernetesInterface().CoreV1().Namespaces().Delete(targetNamespaceName, &metav1.DeleteOptions{})
	require.NoError(t, err)
	err = crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Delete(operatorGroup.Name, &metav1.DeleteOptions{})
	require.NoError(t, err)
}
