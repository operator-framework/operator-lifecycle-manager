package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

func DeploymentComplete(deployment *appsv1.Deployment, newStatus *appsv1.DeploymentStatus) bool {
	return newStatus.UpdatedReplicas == *(deployment.Spec.Replicas) &&
		newStatus.Replicas == *(deployment.Spec.Replicas) &&
		newStatus.AvailableReplicas == *(deployment.Spec.Replicas) &&
		newStatus.ObservedGeneration >= deployment.Generation
}

// Currently this function only modifies the watchedNamespace in the container command
func patchOlmDeployment(t *testing.T, c operatorclient.ClientInterface, newNamespace string) (cleanupFunc func() error) {
	runningDeploy, err := c.GetDeployment(operatorNamespace, "olm-operator")
	require.NoError(t, err)

	oldCommand := runningDeploy.Spec.Template.Spec.Containers[0].Command
	re, err := regexp.Compile(`-watchedNamespaces\W(\S+)`)
	require.NoError(t, err)
	newCommand := re.ReplaceAllString(strings.Join(oldCommand, " "), "$0"+","+newNamespace)
	t.Logf("original=%#v newCommand=%#v", oldCommand, newCommand)
	finalNewCommand := strings.Split(newCommand, " ")
	runningDeploy.Spec.Template.Spec.Containers[0].Command = make([]string, len(finalNewCommand))
	copy(runningDeploy.Spec.Template.Spec.Containers[0].Command, finalNewCommand)

	olmDeployment, updated, err := c.UpdateDeployment(runningDeploy)
	if err != nil || updated == false {
		t.Fatalf("Deployment update failed: (updated %v) %v\n", updated, err)
	}
	require.NoError(t, err)

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		t.Log("Polling for OLM deployment update...")
		fetchedDeployment, err := c.GetDeployment(olmDeployment.Namespace, olmDeployment.Name)
		if err != nil {
			return false, err
		}
		if DeploymentComplete(olmDeployment, &fetchedDeployment.Status) {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	return func() error {
		olmDeployment.Spec.Template.Spec.Containers[0].Command = oldCommand
		_, updated, err := c.UpdateDeployment(olmDeployment)
		if err != nil || updated == false {
			t.Fatalf("Deployment update failed: (updated %v) %v\n", updated, err)
		}
		if err != nil {
			return err
		}
		return nil
	}
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
	// Create CRD
	// Create CSV in operator namespace
	// Create operator group that watches namespace and uses specific label
	// Verify operator group status contains correct status
	// Verify csv in target namespace exists, has copied status, has annotations
	// Verify deployments have correct namespace annotation
	// (Verify that the operator can operate in the target namespace)

	c := newKubeClient(t)
	crc := newCRClient(t)
	csvName := "another-csv" // must be lowercase for DNS-1123 validation

	matchingLabel := map[string]string{"matchLabel": testNamespace}
	otherNamespaceName := testNamespace + "-namespace-two"
	bothNamespaceNames := otherNamespaceName + "," + testNamespace

	otherNamespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   otherNamespaceName,
			Labels: matchingLabel,
		},
	}
	createdOtherNamespace, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&otherNamespace)
	require.NoError(t, err)

	t.Log("Creating CRD")
	mainCRDPlural := genName("opgroup")
	apiGroup := "opcluster.com"
	mainCRDName := mainCRDPlural + "." + apiGroup
	mainCRD := newCRD(mainCRDName, mainCRDPlural)
	mainCRD.Spec.Group = apiGroup
	cleanupCRD, err := createCRD(c, mainCRD)
	require.NoError(t, err)
	defer cleanupCRD()

	t.Log("Creating CSV")
	aCSV := newCSV(csvName, testNamespace, "", *semver.New("0.0.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, nil, newNginxInstallStrategy("operator-deployment", nil, nil))
	createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(&aCSV)
	require.NoError(t, err)

	t.Log("wait for CSV to succeed")
	_, err = fetchCSV(t, crc, createdCSV.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	t.Log("Creating operator group")
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
		//ServiceAccountName: "default-sa",
	}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Create(&operatorGroup)
	require.NoError(t, err)
	expectedOperatorGroupStatus := v1alpha2.OperatorGroupStatus{
		Namespaces: []string{createdOtherNamespace.GetName(), testNamespace},
	}

	t.Log("Waiting on operator group to have correct status")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, fetchErr := crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Get(operatorGroup.Name, metav1.GetOptions{})
		if fetchErr != nil {
			return false, fetchErr
		}
		if len(fetched.Status.Namespaces) > 0 {
			require.Equal(t, expectedOperatorGroupStatus.Namespaces, fetched.Status.Namespaces)
			return true, nil
		}
		return false, nil
	})

	t.Log("Waiting for expected operator group test APIGroup from CSV")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		// (view role is the last role created, so the rest should exist as well by this point)
		viewRole, fetchErr := c.KubernetesInterface().RbacV1().ClusterRoles().Get("e2e-operator-group-view", metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return false, nil
			}
			t.Logf("Unable to fetch view role: %v", fetchErr.Error())
			return false, fetchErr
		}
		for _, rule := range viewRole.Rules {
			for _, group := range rule.APIGroups {
				if group == apiGroup {
					return true, nil
				}
			}
		}
		return false, nil
	})

	t.Log("Checking for proper generated operator group RBAC roles")
	editRole, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get("e2e-operator-group-edit", metav1.GetOptions{})
	require.NoError(t, err)
	editPolicyRules := []rbacv1.PolicyRule{
		{Verbs: []string{"create", "update", "patch", "delete"}, APIGroups: []string{apiGroup}, Resources: []string{mainCRDPlural}},
	}
	t.Log(editRole)
	require.Equal(t, editPolicyRules, editRole.Rules)

	viewRole, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get("e2e-operator-group-view", metav1.GetOptions{})
	require.NoError(t, err)
	viewPolicyRules := []rbacv1.PolicyRule{
		{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{apiGroup}, Resources: []string{mainCRDPlural}},
	}
	t.Log(viewRole)
	require.Equal(t, viewPolicyRules, viewRole.Rules)

	managerRole, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get("owned-crd-manager-another-csv", metav1.GetOptions{})
	require.NoError(t, err)
	managerPolicyRules := []rbacv1.PolicyRule{
		{Verbs: []string{"*"}, APIGroups: []string{apiGroup}, Resources: []string{mainCRDPlural}},
	}
	t.Log(managerRole)
	require.Equal(t, managerPolicyRules, managerRole.Rules)

	t.Log("Waiting for operator namespace csv to have annotations")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			t.Log(fetchErr.Error())
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, bothNamespaceNames) == nil {
			return true, nil
		}
		return false, nil
	})

	t.Log("Waiting for target namespace csv to have annotations")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return false, nil
			}
			t.Logf("Error (in %v): %v", otherNamespaceName, fetchErr.Error())
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, bothNamespaceNames) == nil {
			return true, nil
		}

		return false, nil
	})
	// since annotations are set along with status, no reason to poll for this check as done above
	t.Log("Checking status on csv in target namespace")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return false, nil
			}
			t.Logf("Error (in %v): %v", otherNamespaceName, fetchErr.Error())
			return false, fetchErr
		}
		if fetchedCSV.Status.Reason == v1alpha1.CSVReasonCopied {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	t.Log("Waiting on deployment to have correct annotations")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		createdDeployment, err := c.GetDeployment(testNamespace, "operator-deployment")
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if checkOperatorGroupAnnotations(&createdDeployment.Spec.Template, &operatorGroup, bothNamespaceNames) == nil {
			return true, nil
		}
		return false, nil
	})

	// ensure deletion cleans up copied CSV
	t.Log("Deleting CSV")
	err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(csvName, &metav1.DeleteOptions{})
	require.NoError(t, err)

	t.Log("Waiting for orphaned CSV to be deleted")
	err = waitForDelete(func() error {
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		return err
	})
	require.NoError(t, err)

	err = c.KubernetesInterface().CoreV1().Namespaces().Delete(otherNamespaceName, &metav1.DeleteOptions{})
	require.NoError(t, err)
	err = crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Delete(operatorGroup.Name, &metav1.DeleteOptions{})
	require.NoError(t, err)
}
