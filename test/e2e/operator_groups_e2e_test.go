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
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/apis/rbac"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
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

func newOperatorGroup(namespace, name string, annotations map[string]string, selector *metav1.LabelSelector, targetNamespaces []string, static bool) *v1alpha2.OperatorGroup {
	return &v1alpha2.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        name,
			Annotations: annotations,
		},
		Spec: v1alpha2.OperatorGroupSpec{
			TargetNamespaces:   targetNamespaces,
			Selector:           selector,
			StaticProvidedAPIs: static,
		},
	}
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
	// Verify the copied CSV transitions to FAILED
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
			Name:   opGroupNamespace,
			Labels: matchingLabel,
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
	mainCRD := newCRD(mainCRDPlural)
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
			Selector: &metav1.LabelSelector{
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
			require.ElementsMatch(t, expectedOperatorGroupStatus.Namespaces, fetched.Status.Namespaces)
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
					APIGroups: []string{mainCRD.Spec.Group},
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

	aCSV := newCSV(csvName, opGroupNamespace, "", *semver.New("0.0.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, namedStrategy)
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
		{Verbs: []string{"*"}, APIGroups: []string{mainCRD.Spec.Group}, Resources: []string{mainCRDPlural}},
	}
	require.Equal(t, adminPolicyRules, adminRole.Rules)

	editRole, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(operatorGroup.Name+"-edit", metav1.GetOptions{})
	require.NoError(t, err)
	editPolicyRules := []rbacv1.PolicyRule{
		{Verbs: []string{"create", "update", "patch", "delete"}, APIGroups: []string{mainCRD.Spec.Group}, Resources: []string{mainCRDPlural}},
	}
	require.Equal(t, editPolicyRules, editRole.Rules)

	viewRole, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(operatorGroup.Name+"-view", metav1.GetOptions{})
	require.NoError(t, err)
	viewPolicyRules := []rbacv1.PolicyRule{
		{Verbs: []string{"get"}, APIGroups: []string{"apiextensions.k8s.io"}, Resources: []string{"customresourcedefinitions"}, ResourceNames: []string{mainCRD.Name}},
		{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{mainCRD.Spec.Group}, Resources: []string{mainCRDPlural}},
	}
	require.Equal(t, viewPolicyRules, viewRole.Rules)

	// Unsupport all InstallModes
	t.Log("unsupporting all csv installmodes")
	fetchedCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(csvName, metav1.GetOptions{})
	require.NoError(t, err, "could not fetch csv")
	fetchedCSV.Spec.InstallModes = []v1alpha1.InstallMode{}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(fetchedCSV.GetNamespace()).Update(fetchedCSV)
	require.NoError(t, err, "could not update csv installmodes")

	// Ensure CSV fails
	_, err = fetchCSV(t, crc, csvName, opGroupNamespace, csvFailedChecker)
	require.NoError(t, err, "csv did not transition to failed as expected")

	// Ensure Failed status was propagated to copied CSV
	_, err = fetchCSV(t, crc, csvName, otherNamespaceName, func(csv *v1alpha1.ClusterServiceVersion) bool {
		return csvFailedChecker(csv) && csv.Status.Reason == v1alpha1.CSVReasonCopied
	})
	require.NoError(t, err, "csv failed status did not propagate to copied csv")

	// ensure deletion cleans up copied CSV
	t.Log("deleting parent csv")
	err = crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Delete(csvName, &metav1.DeleteOptions{})
	require.NoError(t, err)

	t.Log("waiting for orphaned csv to be deleted")
	err = waitForDelete(func() error {
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		return err
	})
	require.NoError(t, err)
}

func TestOperatorGroupInstallModeSupport(t *testing.T) {
	// Generate namespaceA
	// Generate namespaceB
	// Create operatorGroupA in namespaceA that selects namespaceA
	// Generate csvA with an unfulfilled required CRD and no supported InstallModes in namespaceA
	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	// Update csvA to have OwnNamespace supported=true
	// Ensure csvA transitions to Pending
	// Update operatorGroupA's target namespaces to select namespaceB
	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	// Update csvA to have SingleNamespace supported=true
	// Ensure csvA transitions to Pending
	// Update operatorGroupA's target namespaces to select namespaceA and namespaceB
	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	// Update csvA to have MultiNamespace supported=true
	// Ensure csvA transitions to Pending
	// Update operatorGroupA to select all namespaces
	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	// Update csvA to have AllNamespaces supported=true
	// Ensure csvA transitions to Pending

	// Generate namespaceA and namespaceB
	nsA := genName("a")
	nsB := genName("b")

	c := newKubeClient(t)
	for _, ns := range []string{nsA, nsB} {
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		}
		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(namespace)
		require.NoError(t, err)
		defer func(name string) {
			require.NoError(t, c.KubernetesInterface().CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{}))
		}(ns)
	 }

	// Generate operatorGroupA 
	crc := newCRClient(t)
	groupA := newOperatorGroup(nsA, genName("a"), nil, nil, []string{nsA}, false)
	_, err := crc.OperatorsV1alpha2().OperatorGroups(nsA).Create(groupA)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha2().OperatorGroups(nsA).Delete(groupA.GetName(), &metav1.DeleteOptions{}))
	}()

	// Generate csvA in namespaceA with no supported InstallModes
	crd := newCRD(genName("b")) 
	namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	csv := newCSV("nginx-a", nsA, "", *semver.New("0.1.0"), nil, []apiextensions.CustomResourceDefinition{crd}, namedStrategy)
	csvA := &csv
	csvA.Spec.InstallModes = []v1alpha1.InstallMode{
		{
			Type: v1alpha1.InstallModeTypeOwnNamespace,
			Supported: false,
		},
		{
			Type: v1alpha1.InstallModeTypeSingleNamespace,
			Supported: false,
		},
		{
			Type: v1alpha1.InstallModeTypeMultiNamespace,
			Supported: false,
		},
		{
			Type: v1alpha1.InstallModeTypeAllNamespaces,
			Supported: false,
		},
	}
	csvA, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Create(csvA)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Delete(csvA.GetName(), &metav1.DeleteOptions{}))
	}()

	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	failedWithUnsupportedOperatorGroup := func(csv *v1alpha1.ClusterServiceVersion) bool {
		return csvFailedChecker(csv) && csv.Status.Reason == v1alpha1.CSVReasonUnsupportedOperatorGroup
	}
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, failedWithUnsupportedOperatorGroup)
	require.NoError(t, err)

	// Update csvA to have OwnNamespace supported=true
	csvA.Spec.InstallModes = []v1alpha1.InstallMode{
		{
			Type: v1alpha1.InstallModeTypeOwnNamespace,
			Supported: true,
		},
		{
			Type: v1alpha1.InstallModeTypeSingleNamespace,
			Supported: false,
		},
		{
			Type: v1alpha1.InstallModeTypeMultiNamespace,
			Supported: false,
		},
		{
			Type: v1alpha1.InstallModeTypeAllNamespaces,
			Supported: false,
		},
	}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(csvA)
	require.NoError(t, err)

	// Ensure csvA transitions to Pending
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, csvPendingChecker)
	require.NoError(t, err)

	// Update operatorGroupA's target namespaces to select namespaceB
	groupA, err = crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	groupA.Spec.TargetNamespaces = []string{nsB}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(nsA).Update(groupA)
	require.NoError(t, err)

	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, failedWithUnsupportedOperatorGroup)
	require.NoError(t, err)

	// Update csvA to have SingleNamespace supported=true
	csvA.Spec.InstallModes = []v1alpha1.InstallMode{
		{
			Type: v1alpha1.InstallModeTypeOwnNamespace,
			Supported: true,
		},
		{
			Type: v1alpha1.InstallModeTypeSingleNamespace,
			Supported: true,
		},
		{
			Type: v1alpha1.InstallModeTypeMultiNamespace,
			Supported: false,
		},
		{
			Type: v1alpha1.InstallModeTypeAllNamespaces,
			Supported: false,
		},
	}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(csvA)
	require.NoError(t, err)

	// Ensure csvA transitions to Pending
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, csvPendingChecker)
	require.NoError(t, err)

	// Update operatorGroupA's target namespaces to select namespaceA and namespaceB
	groupA, err = crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	groupA.Spec.TargetNamespaces =  []string{nsA, nsB}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(nsA).Update(groupA)
	require.NoError(t, err)

	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, failedWithUnsupportedOperatorGroup)
	require.NoError(t, err)

	// Update csvA to have MultiNamespace supported=true
	csvA.Spec.InstallModes = []v1alpha1.InstallMode{
		{
			Type: v1alpha1.InstallModeTypeOwnNamespace,
			Supported: true,
		},
		{
			Type: v1alpha1.InstallModeTypeSingleNamespace,
			Supported: true,
		},
		{
			Type: v1alpha1.InstallModeTypeMultiNamespace,
			Supported: true,
		},
		{
			Type: v1alpha1.InstallModeTypeAllNamespaces,
			Supported: false,
		},
	}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(csvA)
	require.NoError(t, err)

	// Ensure csvA transitions to Pending
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, csvPendingChecker)
	require.NoError(t, err)

	// Update operatorGroupA's target namespaces to select all namespaces
	groupA, err = crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	groupA.Spec.TargetNamespaces =  []string{}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(nsA).Update(groupA)
	require.NoError(t, err)

	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, failedWithUnsupportedOperatorGroup)
	require.NoError(t, err)

	// Update csvA to have AllNamespaces supported=true
	csvA.Spec.InstallModes = []v1alpha1.InstallMode{
		{
			Type: v1alpha1.InstallModeTypeOwnNamespace,
			Supported: true,
		},
		{
			Type: v1alpha1.InstallModeTypeSingleNamespace,
			Supported: true,
		},
		{
			Type: v1alpha1.InstallModeTypeMultiNamespace,
			Supported: true,
		},
		{
			Type: v1alpha1.InstallModeTypeAllNamespaces,
			Supported: true,
		},
	}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(csvA)
	require.NoError(t, err)

	// Ensure csvA transitions to Pending
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, csvPendingChecker)
	require.NoError(t, err)
}

func TestOperatorGroupIntersection(t *testing.T) {
	// Generate namespaceA
	// Generate namespaceB
	// Generate namespaceC
	// Generate namespaceD
	// Generate namespaceE
	// Generate operatorGroupD in namespaceD that selects namespace D and E
	// Generate csvD in namespaceD
	// Wait for csvD to be successful
	// Wait for csvD to have a CSV with copied status in namespace D
	// Wait for operatorGroupD to have providedAPI annotation with crdD's Kind.version.group
	// Generate operatorGroupA in namespaceA that selects AllNamespaces
	// Generate csvD in namespaceA
	// Wait for csvD to fail with status "InterOperatorGroupOwnerConflict"
	// Ensure operatorGroupA's providedAPIs are empty
	// Ensure csvD in namespaceD is still successful
	// Generate csvA in namespaceA that owns crdA
	// Wait for csvA to be successful
	// Wait for operatorGroupA to have providedAPI annotation with crdA's Kind.version.group in its providedAPIs annotation
	// Wait for csvA to have a CSV with copied status in namespace C
	// Generate operatorGroupB in namespaceB that selects namespace C
	// Generate csvB in namespaceB that owns crdA
	// Wait for csvB to fail with status "InterOperatorGroupOwnerConflict"
	// Delete csvA
	// Wait for crdA's Kind.version.group to be removed from operatorGroupA's providedAPIs annotation
	// Ensure csvA's deployments are deleted
	// Wait for csvB to be successful
	// Wait for operatorGroupB to have providedAPI annotation with crdB's Kind.version.group
	// Wait for csvB to have a CSV with a copied status in namespace C

	// Create a catalog for csvA, csvB, and csvD
	pkgA := genName("a")
	pkgB := genName("b")
	pkgD := genName("d")
	pkgAStable := pkgA + "-stable"
	pkgBStable := pkgB + "-stable"
	pkgDStable := pkgD + "-stable"
	stableChannel := "stable"
	strategyA := newNginxInstallStrategy(pkgAStable, nil, nil)
	strategyB := newNginxInstallStrategy(pkgBStable, nil, nil)
	strategyD := newNginxInstallStrategy(pkgDStable, nil, nil)
	crdA := newCRD(genName(pkgA))
	crdB := newCRD(genName(pkgB))
	crdD := newCRD(genName(pkgD))
	kvgA := fmt.Sprintf("%s.%s.%s", crdA.Spec.Names.Kind, crdA.Spec.Version, crdA.Spec.Group)
	kvgB := fmt.Sprintf("%s.%s.%s", crdB.Spec.Names.Kind, crdB.Spec.Version, crdB.Spec.Group)
	kvgD := fmt.Sprintf("%s.%s.%s", crdD.Spec.Names.Kind, crdD.Spec.Version, crdD.Spec.Group)
	csvA := newCSV(pkgAStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crdA}, nil, strategyA)
	csvB := newCSV(pkgBStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crdA, crdB}, nil, strategyB)
	csvD := newCSV(pkgDStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crdD}, nil, strategyD)

	// Create namespaces
	nsA, nsB, nsC, nsD, nsE := genName("a"), genName("b"), genName("c"), genName("d"), genName("e")
	c := newKubeClient(t)
	crc := newCRClient(t)
	for _, ns := range []string{nsA, nsB, nsC, nsD, nsE} {
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		}
		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(namespace)
		require.NoError(t, err)
		defer func(name string) {
			require.NoError(t, c.KubernetesInterface().CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{}))
		}(ns)
	}

	// Create the initial catalogsources
	manifests := []registry.PackageManifest{
		{
			PackageName: pkgA,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: pkgAStable},
			},
			DefaultChannelName: stableChannel,
		},
		{
			PackageName: pkgB,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: pkgBStable},
			},
			DefaultChannelName: stableChannel,
		},
		{
			PackageName: pkgD,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: pkgDStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	catalog := genName("catalog-")
	_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, catalog, nsA, manifests, []apiextensions.CustomResourceDefinition{crdA, crdD, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB, csvD})
	defer cleanupCatalogSource()
	_, err := fetchCatalogSource(t, crc, catalog, nsA, catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	_, cleanupCatalogSource = createInternalCatalogSource(t, c, crc, catalog, nsB, manifests, []apiextensions.CustomResourceDefinition{crdA, crdD, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB, csvD})
	defer cleanupCatalogSource()
	_, err = fetchCatalogSource(t, crc, catalog, nsB, catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	_, cleanupCatalogSource = createInternalCatalogSource(t, c, crc, catalog, nsD, manifests, []apiextensions.CustomResourceDefinition{crdA, crdD, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB, csvD})
	defer cleanupCatalogSource()
	_, err = fetchCatalogSource(t, crc, catalog, nsD, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Create operatorgroups
	groupA := newOperatorGroup(nsA, genName("a"), nil, nil, nil, false)
	groupB := newOperatorGroup(nsB, genName("b"), nil, nil, []string{nsC}, false)
	groupD := newOperatorGroup(nsD, genName("d"), nil, nil, []string{nsD, nsE}, false)
	for _, group := range []*v1alpha2.OperatorGroup{groupA, groupB, groupD} {
		_, err := crc.OperatorsV1alpha2().OperatorGroups(group.GetNamespace()).Create(group)
		require.NoError(t, err)
		defer func(namespace, name string) {
			require.NoError(t, crc.OperatorsV1alpha2().OperatorGroups(namespace).Delete(name, &metav1.DeleteOptions{}))
		}(group.GetNamespace(), group.GetName())
	}

	// Create subscription for csvD in namespaceD
	subDName := genName("d")
	cleanupSubD := createSubscriptionForCatalog(t, crc, nsD, subDName, catalog, pkgD, stableChannel, pkgDStable, v1alpha1.ApprovalAutomatic)
	defer cleanupSubD()
	subD, err := fetchSubscription(t, crc, nsD, subDName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subD)

	// Await csvD's success
	_, err = awaitCSV(t, crc, nsD, csvD.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Await csvD's copy in namespaceE
	_, err = awaitCSV(t, crc, nsE, csvD.GetName(), csvCopiedChecker)
	require.NoError(t, err)

	// Await annotation on groupD
	q := func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsD).Get(groupD.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: kvgD}))

	// Create subscription for csvD2 in namespaceA
	subD2Name := genName("d")
	cleanupSubD2 := createSubscriptionForCatalog(t, crc, nsA, subD2Name, catalog, pkgD, stableChannel, pkgDStable, v1alpha1.ApprovalAutomatic)
	defer cleanupSubD2()
	subD2, err := fetchSubscription(t, crc, nsA, subD2Name, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subD2)

	// Await csvD2's failure
	csvD2, err := awaitCSV(t, crc, nsA, csvD.GetName(), csvFailedChecker)
	require.NoError(t, err)
	require.Equal(t, v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, csvD2.Status.Reason)

	// Ensure groupA's annotations are blank
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{}))

	// Ensure csvD is still successful
	_, err = awaitCSV(t, crc, nsD, csvD.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Create subscription for csvA in namespaceA
	subAName := genName("a")
	cleanupSubA := createSubscriptionForCatalog(t, crc, nsA, subAName, catalog, pkgA, stableChannel, pkgAStable, v1alpha1.ApprovalAutomatic)
	defer cleanupSubA()
	subA, err := fetchSubscription(t, crc, nsA, subAName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subA)

	// Await csvA's success
	_, err = awaitCSV(t, crc, nsA, csvA.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Await annotation on groupA
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

	// Await csvA's copy in namespaceC
	_, err = awaitCSV(t, crc, nsC, csvA.GetName(), csvCopiedChecker)
	require.NoError(t, err)

	// Create subscription for csvB in namespaceB
	subBName := genName("b")
	cleanupSubB := createSubscriptionForCatalog(t, crc, nsB, subBName, catalog, pkgB, stableChannel, pkgBStable, v1alpha1.ApprovalAutomatic)
	defer cleanupSubB()
	subB, err := fetchSubscription(t, crc, nsB, subBName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subB)

	// Await csvB's failure
	fetchedB, err := awaitCSV(t, crc, nsB, csvB.GetName(), csvFailedChecker)
	require.NoError(t, err)
	require.Equal(t, v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, fetchedB.Status.Reason)

	// Ensure no annotation on groupB
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsB).Get(groupB.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{}))

	// Delete csvA
	require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Delete(csvA.GetName(), &metav1.DeleteOptions{}))

	// Ensure annotations are removed from groupA
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: ""}))

	// Ensure csvA's deployment is deleted
	require.NoError(t, waitForDeploymentToDelete(t, c, pkgAStable))

	// Await csvB's success
	_, err = awaitCSV(t, crc, nsB, csvB.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Await csvB's copy in namespace C
	_, err = awaitCSV(t, crc, nsC, csvB.GetName(), csvCopiedChecker)
	require.NoError(t, err)

	// Ensure annotations exist on group B
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsB).Get(groupB.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: strings.Join([]string{kvgA, kvgB}, ",")}))
}

func TestStaticProviderOperatorGroup(t *testing.T) {
	// Generate namespaceA
	// Generate namespaceB
	// Generate namespaceC
	// Generate namespaceD
	// Create static operatorGroupA in namespaceA that targets namespaceD with providedAPIs annotation containing KindA.version.group
	// Create operatorGroupB in namespaceB that targets all namespaces
	// Create operatorGroupC in namespaceC that targets namespaceC
	// Create csvA in namespaceB that provides KindA.version.group
	// Wait for csvA in namespaceB to fail
	// Ensure no providedAPI annotations on operatorGroupB
	// Ensure providedAPI annotations are unchanged on operatorGroupA
	// Create csvA in namespaceC
	// Wait for csvA in namespaceC to succeed
	// Ensure KindA.version.group providedAPI annotation on operatorGroupC
	// Create csvB in namespaceB that provides KindB.version.group
	// Wait for csvB to succeed
	// Wait for csvB to be copied to namespaceA, namespaceC, and namespaceD
	// Wait for KindB.version.group to exist in operatorGroupB's providedAPIs annotation
	// Add namespaceD to operatorGroupC's targetNamespaces
	// Wait for csvA in namespaceC to FAIL with status "InterOperatorGroupOwnerConflict"
	// Wait for KindA.version.group providedAPI annotation to be removed from operatorGroupC's providedAPIs annotation
	// Ensure KindA.version.group providedAPI annotation on operatorGroupA

	// Create a catalog for csvA, csvB
	pkgA := genName("a")
	pkgB := genName("b")
	pkgAStable := pkgA + "-stable"
	pkgBStable := pkgB + "-stable"
	stableChannel := "stable"
	strategyA := newNginxInstallStrategy(pkgAStable, nil, nil)
	strategyB := newNginxInstallStrategy(pkgBStable, nil, nil)
	crdA := newCRD(genName(pkgA))
	crdB := newCRD(genName(pkgB))
	kvgA := fmt.Sprintf("%s.%s.%s", crdA.Spec.Names.Kind, crdA.Spec.Version, crdA.Spec.Group)
	kvgB := fmt.Sprintf("%s.%s.%s", crdB.Spec.Names.Kind, crdB.Spec.Version, crdB.Spec.Group)
	csvA := newCSV(pkgAStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crdA}, nil, strategyA)
	csvB := newCSV(pkgBStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crdB}, nil, strategyB)

	// Create namespaces
	nsA, nsB, nsC, nsD := genName("a"), genName("b"), genName("c"), genName("d")
	c := newKubeClient(t)
	crc := newCRClient(t)
	for _, ns := range []string{nsA, nsB, nsC, nsD} {
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		}
		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(namespace)
		require.NoError(t, err)
		defer func(name string) {
			require.NoError(t, c.KubernetesInterface().CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{}))
		}(ns)
	}

	// Create the initial catalogsources
	manifests := []registry.PackageManifest{
		{
			PackageName: pkgA,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: pkgAStable},
			},
			DefaultChannelName: stableChannel,
		},
		{
			PackageName: pkgB,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: pkgBStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Create catalog in namespaceB and namespaceC
	catalog := genName("catalog-")
	_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, catalog, nsB, manifests, []apiextensions.CustomResourceDefinition{crdA, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB})
	defer cleanupCatalogSource()
	_, err := fetchCatalogSource(t, crc, catalog, nsB, catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	_, cleanupCatalogSource = createInternalCatalogSource(t, c, crc, catalog, nsC, manifests, []apiextensions.CustomResourceDefinition{crdA, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB})
	defer cleanupCatalogSource()
	_, err = fetchCatalogSource(t, crc, catalog, nsC, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Create OperatorGroups
	groupA := newOperatorGroup(nsA, genName("a"), map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: kvgA}, nil, []string{nsD}, true)
	groupB := newOperatorGroup(nsB, genName("b"), nil, nil, nil, false)
	groupC := newOperatorGroup(nsC, genName("d"), nil, nil, []string{nsC}, false)
	for _, group := range []*v1alpha2.OperatorGroup{groupA, groupB, groupC} {
		_, err := crc.OperatorsV1alpha2().OperatorGroups(group.GetNamespace()).Create(group)
		require.NoError(t, err)
		defer func(namespace, name string) {
			require.NoError(t, crc.OperatorsV1alpha2().OperatorGroups(namespace).Delete(name, &metav1.DeleteOptions{}))
		}(group.GetNamespace(), group.GetName())
	}

	// Create subscription for csvA in namespaceB
	subAName := genName("a")
	cleanupSubA := createSubscriptionForCatalog(t, crc, nsB, subAName, catalog, pkgA, stableChannel, pkgAStable, v1alpha1.ApprovalAutomatic)
	defer cleanupSubA()
	subA, err := fetchSubscription(t, crc, nsB, subAName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subA)

	// Await csvA's failure
	fetchedCSVA, err := awaitCSV(t, crc, nsB, csvA.GetName(), csvFailedChecker)
	require.NoError(t, err)
	require.Equal(t, v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, fetchedCSVA.Status.Reason)

	// Ensure operatorGroupB doesn't have providedAPI annotation
	q := func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsB).Get(groupB.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{}))

	// Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

	// Create subscription for csvA in namespaceC
	cleanupSubAC := createSubscriptionForCatalog(t, crc, nsC, subAName, catalog, pkgA, stableChannel, pkgAStable, v1alpha1.ApprovalAutomatic)
	defer cleanupSubAC()
	subAC, err := fetchSubscription(t, crc, nsC, subAName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subAC)

	// Await csvA's success
	_, err = awaitCSV(t, crc, nsC, csvA.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Ensure operatorGroupC has KindA.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsC).Get(groupC.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

	// Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

	// Create subscription for csvB in namespaceB
	subBName := genName("b")
	cleanupSubB := createSubscriptionForCatalog(t, crc, nsB, subBName, catalog, pkgB, stableChannel, pkgBStable, v1alpha1.ApprovalAutomatic)
	defer cleanupSubB()
	subB, err := fetchSubscription(t, crc, nsB, subBName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subB)

	// Await csvB's success
	_, err = awaitCSV(t, crc, nsB, csvB.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Await copied csvBs
	_, err = awaitCSV(t, crc, nsA, csvB.GetName(), csvCopiedChecker)
	require.NoError(t, err)
	_, err = awaitCSV(t, crc, nsC, csvB.GetName(), csvCopiedChecker)
	require.NoError(t, err)
	_, err = awaitCSV(t, crc, nsD, csvB.GetName(), csvCopiedChecker)
	require.NoError(t, err)

	// Ensure operatorGroupB has KindB.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsB).Get(groupB.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: kvgB}))

	// Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

	// Add namespaceD to operatorGroupC's targetNamespaces
	groupC, err = crc.OperatorsV1alpha2().OperatorGroups(groupC.GetNamespace()).Get(groupC.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	groupC.Spec.TargetNamespaces = []string{nsC, nsD}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(groupC.GetNamespace()).Update(groupC)
	require.NoError(t, err)

	// Wait for csvA in namespaceC to fail with status "InterOperatorGroupOwnerConflict"
	fetchedCSVA, err = awaitCSV(t, crc, nsC, csvA.GetName(), csvFailedChecker)
	require.NoError(t, err)
	require.Equal(t, v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, fetchedCSVA.Status.Reason)

	// Wait for crdA's providedAPIs to be removed from operatorGroupC's providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsC).Get(groupC.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: ""}))

	// Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1alpha2().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1alpha2.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))
}

// TODO: Test OperatorGroup resizing collisions
// TODO: Test Subscriptions with depedencies and transitive dependencies in intersecting OperatorGroups
// TODO: Test Subscription upgrade paths with + and - providedAPIs
func TestCSVCopyWatchingAllNamespaces(t *testing.T) {
	c := newKubeClient(t)
	crc := newCRClient(t)
	csvName := genName("another-csv-") // must be lowercase for DNS-1123 validation

	opGroupNamespace := genName(testNamespace + "-")
	matchingLabel := map[string]string{"inGroup": opGroupNamespace}
	otherNamespaceName := genName(opGroupNamespace + "-")

	_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   opGroupNamespace,
			Labels: matchingLabel,
		},
	})
	require.NoError(t, err)
	defer func() {
		err = c.KubernetesInterface().CoreV1().Namespaces().Delete(opGroupNamespace, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	t.Log("Creating CRD")
	mainCRDPlural := genName("opgroup")
	mainCRD := newCRD(mainCRDPlural)
	cleanupCRD, err := createCRD(c, mainCRD)
	require.NoError(t, err)
	defer cleanupCRD()

	t.Log("Creating operator group")
	operatorGroup := v1alpha2.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("e2e-operator-group-"),
			Namespace: opGroupNamespace,
		},
		// no spec, watches all namespaces
	}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(opGroupNamespace).Create(&operatorGroup)
	require.NoError(t, err)
	defer func() {
		err = crc.OperatorsV1alpha2().OperatorGroups(opGroupNamespace).Delete(operatorGroup.Name, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	expectedOperatorGroupStatus := v1alpha2.OperatorGroupStatus{
		Namespaces: []string{metav1.NamespaceAll},
	}

	t.Log("Waiting on operator group to have correct status")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, fetchErr := crc.OperatorsV1alpha2().OperatorGroups(opGroupNamespace).Get(operatorGroup.Name, metav1.GetOptions{})
		if fetchErr != nil {
			return false, fetchErr
		}
		if len(fetched.Status.Namespaces) > 0 {
			require.ElementsMatch(t, expectedOperatorGroupStatus.Namespaces, fetched.Status.Namespaces)
			fmt.Println(fetched.Status.Namespaces)
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
					APIGroups: []string{mainCRD.Spec.Group},
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

	aCSV := newCSV(csvName, opGroupNamespace, "", *semver.New("0.0.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, namedStrategy)
	aCSV.Labels = map[string]string{"label": t.Name()}
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
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, true, corev1.NamespaceAll) == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	csvList, err := crc.OperatorsV1alpha1().ClusterServiceVersions(corev1.NamespaceAll).List(metav1.ListOptions{LabelSelector: "label=TestCSVCopyWatchingAllNamespaces"})
	require.NoError(t, err)
	t.Logf("Found CSV count of %v", len(csvList.Items))

	t.Log("Create other namespace")
	otherNamespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   otherNamespaceName,
			Labels: matchingLabel,
		},
	}
	_, err = c.KubernetesInterface().CoreV1().Namespaces().Create(&otherNamespace)
	require.NoError(t, err)
	defer func() {
		err = c.KubernetesInterface().CoreV1().Namespaces().Delete(otherNamespaceName, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	t.Log("Waiting to ensure copied CSV shows up in new namespace")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return false, nil
			}
			t.Logf("Error (in %v): %v", testNamespace, fetchErr.Error())
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, false, "") == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	// ensure deletion cleans up copied CSV
	t.Log("deleting parent csv")
	err = crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Delete(csvName, &metav1.DeleteOptions{})
	require.NoError(t, err)

	t.Log("waiting for orphaned csv to be deleted")
	err = waitForDelete(func() error {
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		return err
	})
	require.NoError(t, err)
}
