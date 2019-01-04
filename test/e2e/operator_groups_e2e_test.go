package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/apis/rbac"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
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

func checkOperatorGroupAnnotations(obj metav1.Object, op *v1alpha2.OperatorGroup, checkTargetNamespaces bool, targetNamespaces string) error {
	if checkTargetNamespaces {
		if annotation, ok := obj.GetAnnotations()[v1alpha2.OperatorGroupTargetsAnnotationKey]; !ok || annotation != targetNamespaces {
			return fmt.Errorf("missing targetNamespaces annotation on %v", obj.GetName())
		}
	} else {
		if _, found := obj.GetAnnotations()[v1alpha2.OperatorGroupTargetsAnnotationKey]; found {
			return fmt.Errorf("targetNamespaces annotation unexpectedly found on %v", obj.GetName())
		}
	}

	if annotation, ok := obj.GetAnnotations()[v1alpha2.OperatorGroupNamespaceAnnotationKey]; !ok || annotation != op.GetNamespace() {
		return fmt.Errorf("missing operatorNamespace on %v", obj.GetName())
	}
	if annotation, ok := obj.GetAnnotations()[v1alpha2.OperatorGroupAnnotationKey]; !ok || annotation != op.GetName() {
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
	// Update CSV to support no InstallModes
	// Verify the CSV transitions to FAILED
	// Delete CSV
	// Verify copied CVS is deleted

	c := newKubeClient(t)
	crc := newCRClient(t)
	csvName := genName("another-csv-") // must be lowercase for DNS-1123 validation

	opGroupNamespace := genName(testNamespace + "-")
	matchingLabel := map[string]string{"inGroup": opGroupNamespace}
	otherNamespaceName := genName(opGroupNamespace + "-")
	bothNamespaceNames := opGroupNamespace + "," + otherNamespaceName

	_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: opGroupNamespace,
		},
	})
	require.NoError(t, err)
	defer func() {
		err = c.KubernetesInterface().CoreV1().Namespaces().Delete(opGroupNamespace, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	otherNamespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   otherNamespaceName,
			Labels: matchingLabel,
		},
	}
	createdOtherNamespace, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&otherNamespace)
	require.NoError(t, err)
	defer func() {
		err = c.KubernetesInterface().CoreV1().Namespaces().Delete(otherNamespaceName, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	t.Log("Creating CRD")
	mainCRDPlural := genName("opgroup")
	apiGroup := "opcluster.com"
	mainCRDName := mainCRDPlural + "." + apiGroup
	mainCRD := newCRD(mainCRDName, mainCRDPlural)
	mainCRD.Spec.Group = apiGroup
	cleanupCRD, err := createCRD(c, mainCRD)
	require.NoError(t, err)
	defer cleanupCRD()

	t.Log("Creating operator group")
	operatorGroup := v1alpha2.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("e2e-operator-group-"),
			Namespace: opGroupNamespace,
		},
		Spec: v1alpha2.OperatorGroupSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: matchingLabel,
			},
		},
	}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(opGroupNamespace).Create(&operatorGroup)
	require.NoError(t, err)
	defer func() {
		err = crc.OperatorsV1alpha2().OperatorGroups(opGroupNamespace).Delete(operatorGroup.Name, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	expectedOperatorGroupStatus := v1alpha2.OperatorGroupStatus{
		Namespaces: []string{opGroupNamespace, createdOtherNamespace.GetName()},
	}

	t.Log("Waiting on operator group to have correct status")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, fetchErr := crc.OperatorsV1alpha2().OperatorGroups(opGroupNamespace).Get(operatorGroup.Name, metav1.GetOptions{})
		if fetchErr != nil {
			return false, fetchErr
		}
		if len(fetched.Status.Namespaces) > 0 {
			require.Equal(t, expectedOperatorGroupStatus.Namespaces, fetched.Status.Namespaces)
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	t.Log("Creating CSV")
	// Generate permissions
	serviceAccountName := genName("nginx-sa")
	permissions := []install.StrategyDeploymentPermissions{
		{
			ServiceAccountName: serviceAccountName,
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{rbac.VerbAll},
					APIGroups: []string{apiGroup},
					Resources: []string{mainCRDPlural},
				},
			},
		},
	}

	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: opGroupNamespace,
			Name:      serviceAccountName,
		},
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: opGroupNamespace,
			Name:      serviceAccountName + "-role",
		},
		Rules: permissions[0].Rules,
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: opGroupNamespace,
			Name:      serviceAccountName + "-rb",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: opGroupNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind: "Role",
			Name: role.GetName(),
		},
	}
	_, err = c.CreateServiceAccount(serviceAccount)
	require.NoError(t, err)
	_, err = c.CreateRole(role)
	require.NoError(t, err)
	_, err = c.CreateRoleBinding(roleBinding)
	require.NoError(t, err)

	// Create a new NamedInstallStrategy
	deploymentName := genName("operator-deployment")
	namedStrategy := newNginxInstallStrategy(deploymentName, permissions, nil)

	aCSV := newCSV(csvName, opGroupNamespace, "", *semver.New("0.0.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, nil, namedStrategy)
	createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Create(&aCSV)
	require.NoError(t, err)

	t.Log("wait for CSV to succeed")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(createdCSV.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		t.Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return csvSucceededChecker(fetched), nil
	})
	require.NoError(t, err)

	t.Log("Waiting for operator namespace csv to have annotations")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return false, nil
			}
			t.Logf("Error (in %v): %v", testNamespace, fetchErr.Error())
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, true, bothNamespaceNames) == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	t.Log("Waiting for target namespace csv to have annotations (but not target namespaces)")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return false, nil
			}
			t.Logf("Error (in %v): %v", otherNamespaceName, fetchErr.Error())
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, false, "") == nil {
			return true, nil
		}

		return false, nil
	})

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
		createdDeployment, err := c.GetDeployment(opGroupNamespace, deploymentName)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if checkOperatorGroupAnnotations(&createdDeployment.Spec.Template, &operatorGroup, true, bothNamespaceNames) == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	// check rbac in target namespace
	informerFactory := informers.NewSharedInformerFactory(c.KubernetesInterface(), 1*time.Second)
	roleInformer := informerFactory.Rbac().V1().Roles()
	roleBindingInformer := informerFactory.Rbac().V1().RoleBindings()
	clusterRoleInformer := informerFactory.Rbac().V1().ClusterRoles()
	clusterRoleBindingInformer := informerFactory.Rbac().V1().ClusterRoleBindings()

	// kick off informers
	stopCh := make(chan struct{})
	defer func() {
		stopCh <- struct{}{}
		return
	}()

	for _, informer := range []cache.SharedIndexInformer{roleInformer.Informer(), roleBindingInformer.Informer(), clusterRoleInformer.Informer(), clusterRoleBindingInformer.Informer()} {
		go informer.Run(stopCh)

		synced := func() (bool, error) {
			return informer.HasSynced(), nil
		}

		// wait until the informer has synced to continue
		err := wait.PollUntil(500*time.Millisecond, synced, stopCh)
		require.NoError(t, err)
	}

	ruleChecker := install.NewCSVRuleChecker(roleInformer.Lister(), roleBindingInformer.Lister(), clusterRoleInformer.Lister(), clusterRoleBindingInformer.Lister(), &aCSV)

	t.Log("Waiting for operator to have rbac in target namespace")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		for _, perm := range permissions {
			sa, err := c.GetServiceAccount(opGroupNamespace, perm.ServiceAccountName)
			require.NoError(t, err)
			for _, rule := range perm.Rules {
				satisfied, err := ruleChecker.RuleSatisfied(sa, otherNamespaceName, rule)
				if err != nil {
					t.Log(err.Error())
					return false, nil
				}
				if !satisfied {
					return false, nil
				}
			}
		}
		return true, nil
	})

	// validate provided API clusterroles for the operatorgroup
	adminRole, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(operatorGroup.Name+"-admin", metav1.GetOptions{})
	require.NoError(t, err)
	adminPolicyRules := []rbacv1.PolicyRule{
		{Verbs: []string{"*"}, APIGroups: []string{apiGroup}, Resources: []string{mainCRDPlural}},
	}
	require.Equal(t, adminPolicyRules, adminRole.Rules)

	editRole, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(operatorGroup.Name+"-edit", metav1.GetOptions{})
	require.NoError(t, err)
	editPolicyRules := []rbacv1.PolicyRule{
		{Verbs: []string{"create", "update", "patch", "delete"}, APIGroups: []string{apiGroup}, Resources: []string{mainCRDPlural}},
	}
	require.Equal(t, editPolicyRules, editRole.Rules)

	viewRole, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(operatorGroup.Name+"-view", metav1.GetOptions{})
	require.NoError(t, err)
	viewPolicyRules := []rbacv1.PolicyRule{
		{Verbs: []string{"get"}, APIGroups: []string{"apiextensions.k8s.io"}, Resources: []string{"customresourcedefinitions"}, ResourceNames: []string{mainCRDName}},
		{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{apiGroup}, Resources: []string{mainCRDPlural}},
	}
	require.Equal(t, viewPolicyRules, viewRole.Rules)

	// Unsupport all InstallModes
	fetchedCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(csvName, metav1.GetOptions{})
	require.NoError(t, err, "could not fetch csv")
	fetchedCSV.Spec.InstallModes = []v1alpha1.InstallMode{}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(fetchedCSV.GetNamespace()).Update(fetchedCSV)
	require.NoError(t, err, "could not update csv installmodes")
	_, err = fetchCSV(t, crc, csvName, opGroupNamespace, csvFailedChecker)
	require.NoError(t, err, "csv did not transition to failed as expected")

	// ensure deletion cleans up copied CSV
	t.Log("Deleting CSV")
	err = crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Delete(csvName, &metav1.DeleteOptions{})
	require.NoError(t, err)

	t.Log("Waiting for orphaned CSV to be deleted")
	err = waitForDelete(func() error {
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		return err
	})
	require.NoError(t, err)

}
