package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func checkOperatorGroupAnnotations(obj metav1.Object, op *v1.OperatorGroup, checkTargetNamespaces bool, targetNamespaces string) error {
	if checkTargetNamespaces {
		if annotation, ok := obj.GetAnnotations()[v1.OperatorGroupTargetsAnnotationKey]; !ok || annotation != targetNamespaces {
			return fmt.Errorf("missing targetNamespaces annotation on %v", obj.GetName())
		}
	} else {
		if _, found := obj.GetAnnotations()[v1.OperatorGroupTargetsAnnotationKey]; found {
			return fmt.Errorf("targetNamespaces annotation unexpectedly found on %v", obj.GetName())
		}
	}

	if annotation, ok := obj.GetAnnotations()[v1.OperatorGroupNamespaceAnnotationKey]; !ok || annotation != op.GetNamespace() {
		return fmt.Errorf("missing operatorNamespace on %v", obj.GetName())
	}
	if annotation, ok := obj.GetAnnotations()[v1.OperatorGroupAnnotationKey]; !ok || annotation != op.GetName() {
		return fmt.Errorf("missing operatorGroup annotation on %v", obj.GetName())
	}

	return nil
}

func newOperatorGroup(namespace, name string, annotations map[string]string, selector *metav1.LabelSelector, targetNamespaces []string, static bool) *v1.OperatorGroup {
	return &v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        name,
			Annotations: annotations,
		},
		Spec: v1.OperatorGroupSpec{
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
	defer cleaner.NotifyTestComplete(t, true)

	log := func(s string) {
		t.Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
	}

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

	log("Creating CRD")
	mainCRDPlural := genName("opgroup")
	mainCRD := newCRD(mainCRDPlural)
	cleanupCRD, err := createCRD(c, mainCRD)
	require.NoError(t, err)
	defer cleanupCRD()

	log("Creating operator group")
	operatorGroup := v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("e2e-operator-group-"),
			Namespace: opGroupNamespace,
		},
		Spec: v1.OperatorGroupSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: matchingLabel,
			},
		},
	}
	_, err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Create(&operatorGroup)
	require.NoError(t, err)
	expectedOperatorGroupStatus := v1.OperatorGroupStatus{
		Namespaces: []string{opGroupNamespace, createdOtherNamespace.GetName()},
	}

	log("Waiting on operator group to have correct status")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, fetchErr := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(operatorGroup.Name, metav1.GetOptions{})
		if fetchErr != nil {
			return false, fetchErr
		}
		if len(fetched.Status.Namespaces) > 0 {
			require.ElementsMatch(t, expectedOperatorGroupStatus.Namespaces, fetched.Status.Namespaces, "have %#v", fetched.Status.Namespaces)
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	log("Creating CSV")

	// Generate permissions
	serviceAccountName := genName("nginx-sa")
	permissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: serviceAccountName,
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{rbacv1.VerbAll},
					APIGroups: []string{mainCRD.Spec.Group},
					Resources: []string{mainCRDPlural},
				},
			},
		},
	}

	// Create a new NamedInstallStrategy
	deploymentName := genName("operator-deployment")
	namedStrategy := newNginxInstallStrategy(deploymentName, permissions, nil)

	aCSV := newCSV(csvName, opGroupNamespace, "", semver.MustParse("0.0.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, namedStrategy)
	createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Create(&aCSV)
	require.NoError(t, err)

	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: opGroupNamespace,
			Name:      serviceAccountName,
		},
	}
	ownerutil.AddNonBlockingOwner(serviceAccount, createdCSV)
	err = ownerutil.AddOwnerLabels(serviceAccount, createdCSV)
	require.NoError(t, err)

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: opGroupNamespace,
			Name:      serviceAccountName + "-role",
		},
		Rules: permissions[0].Rules,
	}
	ownerutil.AddNonBlockingOwner(role, createdCSV)
	err = ownerutil.AddOwnerLabels(role, createdCSV)
	require.NoError(t, err)

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
	ownerutil.AddNonBlockingOwner(roleBinding, createdCSV)
	err = ownerutil.AddOwnerLabels(roleBinding, createdCSV)
	require.NoError(t, err)

	_, err = c.CreateServiceAccount(serviceAccount)
	require.NoError(t, err)
	_, err = c.CreateRole(role)
	require.NoError(t, err)
	_, err = c.CreateRoleBinding(roleBinding)
	require.NoError(t, err)

	log("wait for CSV to succeed")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(createdCSV.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
		return csvSucceededChecker(fetched), nil
	})
	require.NoError(t, err)

	log("Waiting for operator namespace csv to have annotations")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return false, nil
			}
			log(fmt.Sprintf("Error (in %v): %v", testNamespace, fetchErr.Error()))
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, true, bothNamespaceNames) == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	log("Waiting for target namespace csv to have annotations (but not target namespaces)")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return false, nil
			}
			log(fmt.Sprintf("Error (in %v): %v", otherNamespaceName, fetchErr.Error()))
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, false, "") == nil {
			return true, nil
		}

		return false, nil
	})

	log("Checking status on csv in target namespace")
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

	log("Waiting on deployment to have correct annotations")
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

	log("Waiting for operator to have rbac in target namespace")
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
	log("unsupporting all csv installmodes")
	fetchedCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(csvName, metav1.GetOptions{})
	require.NoError(t, err, "could not fetch csv")
	fetchedCSV.Spec.InstallModes = []v1alpha1.InstallMode{}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(fetchedCSV.GetNamespace()).Update(fetchedCSV)
	require.NoError(t, err, "could not update csv installmodes")

	// Ensure CSV fails
	_, err = fetchCSV(t, crc, csvName, opGroupNamespace, csvFailedChecker)
	require.NoError(t, err, "csv did not transition to failed as expected")

	// ensure deletion cleans up copied CSV
	log("deleting parent csv")
	err = crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Delete(csvName, &metav1.DeleteOptions{})
	require.NoError(t, err)

	log("waiting for orphaned csv to be deleted")
	err = waitForDelete(func() error {
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		return err
	})
	require.NoError(t, err)

	err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Delete(operatorGroup.Name, &metav1.DeleteOptions{})
	require.NoError(t, err)
	t.Log("Waiting for OperatorGroup RBAC to be garbage collected")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(operatorGroup.Name+"-admin", metav1.GetOptions{})
		if err == nil {
			return false, nil
		}
		return true, err
	})
	require.True(t, errors.IsNotFound(err))

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(operatorGroup.Name+"-edit", metav1.GetOptions{})
		if err == nil {
			return false, nil
		}
		return true, err
	})
	require.True(t, errors.IsNotFound(err))

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(operatorGroup.Name+"-view", metav1.GetOptions{})
		if err == nil {
			return false, nil
		}
		return true, err
	})
	require.True(t, errors.IsNotFound(err))
}

func createProjectAdmin(t *testing.T, c operatorclient.ClientInterface, namespace string) (string, cleanupFunc) {
	sa, err := c.CreateServiceAccount(&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      genName("padmin-"),
		},
	})
	require.NoError(t, err)

	rb, err := c.CreateRoleBinding(&rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("padmin-"),
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "admin",
		},
	})
	require.NoError(t, err)
	// kubectl -n a8v4sw  auth can-i create alp999.cluster.com --as system:serviceaccount:a8v4sw:padmin-xqdfz
	return "system:serviceaccount:" + namespace + ":" + sa.GetName(), func() {
		_ = c.DeleteServiceAccount(sa.GetNamespace(), sa.GetName(), metav1.NewDeleteOptions(0))
		_ = c.DeleteRoleBinding(rb.GetNamespace(), rb.GetName(), metav1.NewDeleteOptions(0))
	}
}

func TestOperatorGroupRoleAggregation(t *testing.T) {
	// Generate namespaceA
	// Generate operatorGroupA - OwnNamespace
	// Generate csvA in namespaceA with all installmodes supported
	// Create crd so csv succeeds
	// Ensure clusterroles created and aggregated for access provided APIs

	defer cleaner.NotifyTestComplete(t, true)

	// Generate namespaceA
	nsA := genName("a")
	c := newKubeClient(t)
	for _, ns := range []string{nsA} {
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

	// Generate operatorGroupA - OwnNamespace
	crc := newCRClient(t)
	groupA := newOperatorGroup(nsA, genName("a"), nil, nil, []string{nsA}, false)
	_, err := crc.OperatorsV1().OperatorGroups(nsA).Create(groupA)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, crc.OperatorsV1().OperatorGroups(nsA).Delete(groupA.GetName(), &metav1.DeleteOptions{}))
	}()

	// Generate csvA in namespaceA with all installmodes supported
	crd := newCRD(genName("a"))
	namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	csvA := newCSV("nginx-a", nsA, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Create(&csvA)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Delete(csvA.GetName(), &metav1.DeleteOptions{}))
	}()

	// Create crd so csv succeeds
	cleanupCRD, err := createCRD(c, crd)
	require.NoError(t, err)
	defer cleanupCRD()

	_, err = fetchCSV(t, crc, csvA.GetName(), nsA, csvSucceededChecker)
	require.NoError(t, err)

	// Create a csv for an apiserver
	depName := genName("hat-server")
	mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
	version := "v1alpha1"
	mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
	mockKinds := []string{"fez", "fedora"}
	mockNames := []string{"fezs", "fedoras"}
	depSpec := newMockExtServerDeployment(depName, mockGroupVersion, mockKinds)
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: depName,
				Spec: depSpec,
			},
		},
	}

	owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
	for i, kind := range mockKinds {
		owned[i] = v1alpha1.APIServiceDescription{
			Name:           mockNames[i],
			Group:          mockGroup,
			Version:        version,
			Kind:           kind,
			DeploymentName: depName,
			ContainerPort:  int32(5443),
			DisplayName:    kind,
			Description:    fmt.Sprintf("A %s", kind),
		}
	}

	csvB := v1alpha1.ClusterServiceVersion{
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned: owned,
			},
		},
	}
	csvB.SetName(depName)

	// Create the APIService CSV
	cleanupCSV, err := createCSV(t, c, crc, csvB, nsA, false, true)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csvB.GetName(), nsA, csvSucceededChecker)
	require.NoError(t, err)

	// Ensure clusterroles created and aggregated for access provided APIs
	padmin, cleanupPadmin := createProjectAdmin(t, c, nsA)
	defer cleanupPadmin()

	// Check CRD access aggregated
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(&authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User: padmin,
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: nsA,
					Group:     crd.Spec.Group,
					Version:   crd.Spec.Version,
					Resource:  crd.Spec.Names.Plural,
					Verb:      "create",
				},
			},
		})
		if err != nil {
			return false, err
		}
		if res == nil {
			return false, nil
		}
		t.Log("checking padmin for permission")
		return res.Status.Allowed, nil
	})
	require.NoError(t, err)

	// Check apiserver access aggregated
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(&authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User: padmin,
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: nsA,
					Group:     mockGroup,
					Version:   version,
					Resource:  mockNames[1],
					Verb:      "create",
				},
			},
		})
		if err != nil {
			return false, err
		}
		if res == nil {
			return false, nil
		}
		t.Logf("checking padmin for permission: %#v", res)
		return res.Status.Allowed, nil
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
	// Ensure csvA transitions to Succeeded
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

	defer cleaner.NotifyTestComplete(t, true)

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
	_, err := crc.OperatorsV1().OperatorGroups(nsA).Create(groupA)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, crc.OperatorsV1().OperatorGroups(nsA).Delete(groupA.GetName(), &metav1.DeleteOptions{}))
	}()

	// Generate csvA in namespaceA with no supported InstallModes
	crd := newCRD(genName("b"))
	namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	csv := newCSV("nginx-a", nsA, "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{crd}, namedStrategy)
	csvA := &csv
	csvA.Spec.InstallModes = []v1alpha1.InstallMode{
		{
			Type:      v1alpha1.InstallModeTypeOwnNamespace,
			Supported: false,
		},
		{
			Type:      v1alpha1.InstallModeTypeSingleNamespace,
			Supported: false,
		},
		{
			Type:      v1alpha1.InstallModeTypeMultiNamespace,
			Supported: false,
		},
		{
			Type:      v1alpha1.InstallModeTypeAllNamespaces,
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
			Type:      v1alpha1.InstallModeTypeOwnNamespace,
			Supported: true,
		},
		{
			Type:      v1alpha1.InstallModeTypeSingleNamespace,
			Supported: false,
		},
		{
			Type:      v1alpha1.InstallModeTypeMultiNamespace,
			Supported: false,
		},
		{
			Type:      v1alpha1.InstallModeTypeAllNamespaces,
			Supported: false,
		},
	}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(csvA)
	require.NoError(t, err)

	// Create crd so csv succeeds
	cleanupCRD, err := createCRD(c, crd)
	require.NoError(t, err)
	defer cleanupCRD()

	// Ensure csvA transitions to Succeeded
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, csvSucceededChecker)
	require.NoError(t, err)

	// Update operatorGroupA's target namespaces to select namespaceB
	groupA, err = crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	groupA.Spec.TargetNamespaces = []string{nsB}
	_, err = crc.OperatorsV1().OperatorGroups(nsA).Update(groupA)
	require.NoError(t, err)

	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, failedWithUnsupportedOperatorGroup)
	require.NoError(t, err)

	// Update csvA to have SingleNamespace supported=true
	csvA.Spec.InstallModes = []v1alpha1.InstallMode{
		{
			Type:      v1alpha1.InstallModeTypeOwnNamespace,
			Supported: true,
		},
		{
			Type:      v1alpha1.InstallModeTypeSingleNamespace,
			Supported: true,
		},
		{
			Type:      v1alpha1.InstallModeTypeMultiNamespace,
			Supported: false,
		},
		{
			Type:      v1alpha1.InstallModeTypeAllNamespaces,
			Supported: false,
		},
	}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(csvA)
	require.NoError(t, err)

	// Ensure csvA transitions to Succeeded
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, csvSucceededChecker)
	require.NoError(t, err)

	// Update operatorGroupA's target namespaces to select namespaceA and namespaceB
	groupA, err = crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	groupA.Spec.TargetNamespaces = []string{nsA, nsB}
	_, err = crc.OperatorsV1().OperatorGroups(nsA).Update(groupA)
	require.NoError(t, err)

	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, failedWithUnsupportedOperatorGroup)
	require.NoError(t, err)

	// Update csvA to have MultiNamespace supported=true
	csvA.Spec.InstallModes = []v1alpha1.InstallMode{
		{
			Type:      v1alpha1.InstallModeTypeOwnNamespace,
			Supported: true,
		},
		{
			Type:      v1alpha1.InstallModeTypeSingleNamespace,
			Supported: true,
		},
		{
			Type:      v1alpha1.InstallModeTypeMultiNamespace,
			Supported: true,
		},
		{
			Type:      v1alpha1.InstallModeTypeAllNamespaces,
			Supported: false,
		},
	}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(csvA)
	require.NoError(t, err)

	// Ensure csvA transitions to Succeeded
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, csvSucceededChecker)
	require.NoError(t, err)

	// Update operatorGroupA's target namespaces to select all namespaces
	groupA, err = crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	groupA.Spec.TargetNamespaces = []string{}
	_, err = crc.OperatorsV1().OperatorGroups(nsA).Update(groupA)
	require.NoError(t, err)

	// Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, failedWithUnsupportedOperatorGroup)
	require.NoError(t, err)

	// Update csvA to have AllNamespaces supported=true
	csvA.Spec.InstallModes = []v1alpha1.InstallMode{
		{
			Type:      v1alpha1.InstallModeTypeOwnNamespace,
			Supported: true,
		},
		{
			Type:      v1alpha1.InstallModeTypeSingleNamespace,
			Supported: true,
		},
		{
			Type:      v1alpha1.InstallModeTypeMultiNamespace,
			Supported: true,
		},
		{
			Type:      v1alpha1.InstallModeTypeAllNamespaces,
			Supported: true,
		},
	}
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(csvA)
	require.NoError(t, err)

	// Ensure csvA transitions to Pending
	csvA, err = fetchCSV(t, crc, csvA.GetName(), nsA, csvSucceededChecker)
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
	// Ensure clusterroles created and aggregated for accessing provided APIs
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

	defer cleaner.NotifyTestComplete(t, true)

	// Create a catalog for csvA, csvB, and csvD
	pkgA := genName("a-")
	pkgB := genName("b-")
	pkgD := genName("d-")
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
	csvA := newCSV(pkgAStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crdA}, nil, strategyA)
	csvB := newCSV(pkgBStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crdA, crdB}, nil, strategyB)
	csvD := newCSV(pkgDStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crdD}, nil, strategyD)

	// Create namespaces
	nsA, nsB, nsC, nsD, nsE := genName("a-"), genName("b-"), genName("c-"), genName("d-"), genName("e-")
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
	groupA := newOperatorGroup(nsA, genName("a-"), nil, nil, nil, false)
	groupB := newOperatorGroup(nsB, genName("b-"), nil, nil, []string{nsC}, false)
	groupD := newOperatorGroup(nsD, genName("d-"), nil, nil, []string{nsD, nsE}, false)
	for _, group := range []*v1.OperatorGroup{groupA, groupB, groupD} {
		_, err := crc.OperatorsV1().OperatorGroups(group.GetNamespace()).Create(group)
		require.NoError(t, err)
		defer func(namespace, name string) {
			require.NoError(t, crc.OperatorsV1().OperatorGroups(namespace).Delete(name, &metav1.DeleteOptions{}))
		}(group.GetNamespace(), group.GetName())
	}

	// Create subscription for csvD in namespaceD
	subDName := genName("d-")
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
		g, err := crc.OperatorsV1().OperatorGroups(nsD).Get(groupD.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgD}))

	// Create subscription for csvD2 in namespaceA
	subD2Name := genName("d2-")
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
		g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{}))

	// Ensure csvD is still successful
	_, err = awaitCSV(t, crc, nsD, csvD.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Create subscription for csvA in namespaceA
	subAName := genName("a-")
	cleanupSubA := createSubscriptionForCatalog(t, crc, nsA, subAName, catalog, pkgA, stableChannel, pkgAStable, v1alpha1.ApprovalAutomatic)
	defer cleanupSubA()
	subA, err := fetchSubscription(t, crc, nsA, subAName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subA)

	// Await csvA's success
	_, err = awaitCSV(t, crc, nsA, csvA.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Ensure clusterroles created and aggregated for access provided APIs
	padmin, cleanupPadmin := createProjectAdmin(t, c, nsA)
	defer cleanupPadmin()

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(&authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User: padmin,
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: nsA,
					Group:     crdA.Spec.Group,
					Version:   crdA.Spec.Version,
					Resource:  crdA.Spec.Names.Plural,
					Verb:      "create",
				},
			},
		})
		if err != nil {
			return false, err
		}
		if res == nil {
			return false, nil
		}
		t.Log("checking padmin for permission")
		return res.Status.Allowed, nil
	})
	require.NoError(t, err)

	// Await annotation on groupA
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

	// Await csvA's copy in namespaceC
	_, err = awaitCSV(t, crc, nsC, csvA.GetName(), csvCopiedChecker)
	require.NoError(t, err)

	// Create subscription for csvB in namespaceB
	subBName := genName("b-")
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
		g, err := crc.OperatorsV1().OperatorGroups(nsB).Get(groupB.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{}))

	// Delete csvA
	require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Delete(csvA.GetName(), &metav1.DeleteOptions{}))

	// Ensure annotations are removed from groupA
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: ""}))

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
		g, err := crc.OperatorsV1().OperatorGroups(nsB).Get(groupB.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: strings.Join([]string{kvgA, kvgB}, ",")}))
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

	defer cleaner.NotifyTestComplete(t, true)

	// Create a catalog for csvA, csvB
	pkgA := genName("a-")
	pkgB := genName("b-")
	pkgAStable := pkgA + "-stable"
	pkgBStable := pkgB + "-stable"
	stableChannel := "stable"
	strategyA := newNginxInstallStrategy(pkgAStable, nil, nil)
	strategyB := newNginxInstallStrategy(pkgBStable, nil, nil)
	crdA := newCRD(genName(pkgA))
	crdB := newCRD(genName(pkgB))
	kvgA := fmt.Sprintf("%s.%s.%s", crdA.Spec.Names.Kind, crdA.Spec.Version, crdA.Spec.Group)
	kvgB := fmt.Sprintf("%s.%s.%s", crdB.Spec.Names.Kind, crdB.Spec.Version, crdB.Spec.Group)
	csvA := newCSV(pkgAStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crdA}, nil, strategyA)
	csvB := newCSV(pkgBStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crdB}, nil, strategyB)

	// Create namespaces
	nsA, nsB, nsC, nsD := genName("a-"), genName("b-"), genName("c-"), genName("d-")
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
	groupA := newOperatorGroup(nsA, genName("a-"), map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}, nil, []string{nsD}, true)
	groupB := newOperatorGroup(nsB, genName("b-"), nil, nil, nil, false)
	groupC := newOperatorGroup(nsC, genName("d-"), nil, nil, []string{nsC}, false)
	for _, group := range []*v1.OperatorGroup{groupA, groupB, groupC} {
		_, err := crc.OperatorsV1().OperatorGroups(group.GetNamespace()).Create(group)
		require.NoError(t, err)
		defer func(namespace, name string) {
			require.NoError(t, crc.OperatorsV1().OperatorGroups(namespace).Delete(name, &metav1.DeleteOptions{}))
		}(group.GetNamespace(), group.GetName())
	}

	// Create subscription for csvA in namespaceB
	subAName := genName("a-")
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
		g, err := crc.OperatorsV1().OperatorGroups(nsB).Get(groupB.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{}))

	// Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

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
		g, err := crc.OperatorsV1().OperatorGroups(nsC).Get(groupC.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

	// Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

	// Create subscription for csvB in namespaceB
	subBName := genName("b-")
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
		g, err := crc.OperatorsV1().OperatorGroups(nsB).Get(groupB.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgB}))

	// Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

	// Add namespaceD to operatorGroupC's targetNamespaces
	groupC, err = crc.OperatorsV1().OperatorGroups(groupC.GetNamespace()).Get(groupC.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	groupC.Spec.TargetNamespaces = []string{nsC, nsD}
	_, err = crc.OperatorsV1().OperatorGroups(groupC.GetNamespace()).Update(groupC)
	require.NoError(t, err)

	// Wait for csvA in namespaceC to fail with status "InterOperatorGroupOwnerConflict"
	fetchedCSVA, err = awaitCSV(t, crc, nsC, csvA.GetName(), csvFailedChecker)
	require.NoError(t, err)
	require.Equal(t, v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, fetchedCSVA.Status.Reason)

	// Wait for crdA's providedAPIs to be removed from operatorGroupC's providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1().OperatorGroups(nsC).Get(groupC.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: ""}))

	// Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation
	q = func() (metav1.ObjectMeta, error) {
		g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(groupA.GetName(), metav1.GetOptions{})
		return g.ObjectMeta, err
	}
	require.NoError(t, awaitAnnotations(t, q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))
}

// TODO: Test OperatorGroup resizing collisions
// TODO: Test Subscriptions with depedencies and transitive dependencies in intersecting OperatorGroups
// TODO: Test Subscription upgrade paths with + and - providedAPIs
func TestCSVCopyWatchingAllNamespaces(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)
	c := newKubeClient(t)
	crc := newCRClient(t)
	csvName := genName("another-csv-") // must be lowercase for DNS-1123 validation

	opGroupNamespace := testNamespace
	matchingLabel := map[string]string{"inGroup": opGroupNamespace}
	otherNamespaceName := genName(opGroupNamespace + "-")

	t.Log("Creating CRD")
	mainCRDPlural := genName("opgroup-")
	mainCRD := newCRD(mainCRDPlural)
	cleanupCRD, err := createCRD(c, mainCRD)
	require.NoError(t, err)
	defer cleanupCRD()

	t.Logf("Getting default operator group 'global-operators' installed via operatorgroup-default.yaml %v", opGroupNamespace)
	operatorGroup, err := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get("global-operators", metav1.GetOptions{})
	require.NoError(t, err)

	expectedOperatorGroupStatus := v1.OperatorGroupStatus{
		Namespaces: []string{metav1.NamespaceAll},
	}

	t.Log("Waiting on operator group to have correct status")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, fetchErr := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(operatorGroup.Name, metav1.GetOptions{})
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
	permissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: serviceAccountName,
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{rbacv1.VerbAll},
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
	defer func() {
		c.DeleteServiceAccount(serviceAccount.GetNamespace(), serviceAccount.GetName(), metav1.NewDeleteOptions(0))
	}()
	createdRole, err := c.CreateRole(role)
	require.NoError(t, err)
	defer func() {
		c.DeleteRole(role.GetNamespace(), role.GetName(), metav1.NewDeleteOptions(0))
	}()
	createdRoleBinding, err := c.CreateRoleBinding(roleBinding)
	require.NoError(t, err)
	defer func() {
		c.DeleteRoleBinding(roleBinding.GetNamespace(), roleBinding.GetName(), metav1.NewDeleteOptions(0))
	}()
	// Create a new NamedInstallStrategy
	deploymentName := genName("operator-deployment")
	namedStrategy := newNginxInstallStrategy(deploymentName, permissions, nil)

	aCSV := newCSV(csvName, opGroupNamespace, "", semver.MustParse("0.0.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, namedStrategy)
	aCSV.Labels = map[string]string{"label": t.Name()}
	createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Create(&aCSV)
	require.NoError(t, err)

	err = ownerutil.AddOwnerLabels(createdRole, createdCSV)
	require.NoError(t, err)
	_, err = c.UpdateRole(createdRole)
	require.NoError(t, err)

	err = ownerutil.AddOwnerLabels(createdRoleBinding, createdCSV)
	require.NoError(t, err)
	_, err = c.UpdateRoleBinding(createdRoleBinding)
	require.NoError(t, err)

	t.Log("wait for CSV to succeed")
	_, err = fetchCSV(t, crc, createdCSV.GetName(), opGroupNamespace, csvSucceededChecker)
	require.NoError(t, err)

	t.Log("wait for roles to be promoted to clusterroles")
	var fetchedRole *rbacv1.ClusterRole
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedRole, err = c.GetClusterRole(role.GetName())
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	require.EqualValues(t, append(role.Rules, rbacv1.PolicyRule{
		Verbs:     []string{"get", "list", "watch"},
		APIGroups: []string{""},
		Resources: []string{"namespaces"},
	}), fetchedRole.Rules)
	var fetchedRoleBinding *rbacv1.ClusterRoleBinding
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedRoleBinding, err = c.GetClusterRoleBinding(roleBinding.GetName())
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	require.EqualValues(t, roleBinding.Subjects, fetchedRoleBinding.Subjects)
	require.EqualValues(t, roleBinding.RoleRef.Name, fetchedRoleBinding.RoleRef.Name)
	require.EqualValues(t, "rbac.authorization.k8s.io", fetchedRoleBinding.RoleRef.APIGroup)
	require.EqualValues(t, "ClusterRole", fetchedRoleBinding.RoleRef.Kind)

	t.Log("ensure operator was granted namespace list permission")
	res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(&authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User: "system:serviceaccount:" + opGroupNamespace + ":" + serviceAccountName,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:    corev1.GroupName,
				Version:  "v1",
				Resource: "namespaces",
				Verb:     "list",
			},
		},
	})
	require.NoError(t, err)
	require.True(t, res.Status.Allowed, "got %#v", res.Status)

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
		if checkOperatorGroupAnnotations(fetchedCSV, operatorGroup, true, corev1.NamespaceAll) == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	csvList, err := crc.OperatorsV1alpha1().ClusterServiceVersions(corev1.NamespaceAll).List(metav1.ListOptions{LabelSelector: fmt.Sprintf("label=%s", t.Name())})
	require.NoError(t, err)
	t.Logf("Found CSV count of %v", len(csvList.Items))

	t.Logf("Create other namespace %s", otherNamespaceName)
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

	t.Log("Waiting to ensure copied CSV shows up in other namespace")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return false, nil
			}
			t.Logf("Error (in %v): %v", otherNamespaceName, fetchErr.Error())
			return false, fetchErr
		}
		if checkOperatorGroupAnnotations(fetchedCSV, operatorGroup, false, "") == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	// verify created CSV is cleaned up after operator group is "contracted"
	t.Log("Modifying operator group to no longer watch all namespaces")
	currentOperatorGroup, err := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(operatorGroup.Name, metav1.GetOptions{})
	require.NoError(t, err)
	currentOperatorGroup.Spec.TargetNamespaces = []string{opGroupNamespace}
	_, err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Update(currentOperatorGroup)
	require.NoError(t, err)
	defer func() {
		t.Log("Re-modifying operator group to be watching all namespaces")
		currentOperatorGroup, err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(operatorGroup.Name, metav1.GetOptions{})
		require.NoError(t, err)
		currentOperatorGroup.Spec = v1.OperatorGroupSpec{}
		_, err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Update(currentOperatorGroup)
		require.NoError(t, err)
	}()

	err = wait.Poll(pollInterval, 2*pollDuration, func() (bool, error) {
		_, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(csvName, metav1.GetOptions{})
		if fetchErr != nil {
			if errors.IsNotFound(fetchErr) {
				return true, nil
			}
			t.Logf("Error (in %v): %v", opGroupNamespace, fetchErr.Error())
			return false, fetchErr
		}
		return false, nil
	})
	require.NoError(t, err)
}

func TestOperatorGroupInsufficientPermissionsResolveViaRBAC(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	log := func(s string) {
		t.Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
	}

	c := newKubeClient(t)
	crc := newCRClient(t)
	csvName := genName("another-csv-")

	newNamespaceName := genName(testNamespace + "-")

	_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: newNamespaceName,
		},
	})
	require.NoError(t, err)
	defer func() {
		err = c.KubernetesInterface().CoreV1().Namespaces().Delete(newNamespaceName, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	log("Creating CRD")
	mainCRDPlural := genName("opgroup")
	mainCRD := newCRD(mainCRDPlural)
	cleanupCRD, err := createCRD(c, mainCRD)
	require.NoError(t, err)
	defer cleanupCRD()

	log("Creating operator group")
	serviceAccountName := genName("nginx-sa")
	// intentionally creating an operator group without a service account already existing
	operatorGroup := v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("e2e-operator-group-"),
			Namespace: newNamespaceName,
		},
		Spec: v1.OperatorGroupSpec{
			ServiceAccountName: serviceAccountName,
			TargetNamespaces:   []string{newNamespaceName},
		},
	}
	_, err = crc.OperatorsV1().OperatorGroups(newNamespaceName).Create(&operatorGroup)
	require.NoError(t, err)

	log("Creating CSV")

	// Create a new NamedInstallStrategy
	deploymentName := genName("operator-deployment")
	namedStrategy := newNginxInstallStrategy(deploymentName, nil, nil)

	aCSV := newCSV(csvName, newNamespaceName, "", semver.MustParse("0.0.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, namedStrategy)
	createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Create(&aCSV)
	require.NoError(t, err)

	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: newNamespaceName,
			Name:      serviceAccountName,
		},
	}
	ownerutil.AddNonBlockingOwner(serviceAccount, createdCSV)
	err = ownerutil.AddOwnerLabels(serviceAccount, createdCSV)
	require.NoError(t, err)

	_, err = c.CreateServiceAccount(serviceAccount)
	require.NoError(t, err)

	log("wait for CSV to fail")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Get(createdCSV.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
		return csvFailedChecker(fetched), nil
	})
	require.NoError(t, err)

	// now add cluster admin permissions to service account
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: newNamespaceName,
			Name:      serviceAccountName + "-role",
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"*"},
				APIGroups: []string{"*"},
				Resources: []string{"*"},
			},
		},
	}
	ownerutil.AddNonBlockingOwner(role, createdCSV)
	err = ownerutil.AddOwnerLabels(role, createdCSV)
	require.NoError(t, err)

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: newNamespaceName,
			Name:      serviceAccountName + "-rb",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: newNamespaceName,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind: "Role",
			Name: role.GetName(),
		},
	}
	ownerutil.AddNonBlockingOwner(roleBinding, createdCSV)
	err = ownerutil.AddOwnerLabels(roleBinding, createdCSV)
	require.NoError(t, err)

	_, err = c.CreateRole(role)
	require.NoError(t, err)
	_, err = c.CreateRoleBinding(roleBinding)
	require.NoError(t, err)

	log("wait for CSV to succeeed")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Get(createdCSV.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
		return csvSucceededChecker(fetched), nil
	})
	require.NoError(t, err)
}

func TestOperatorGroupInsufficientPermissionsResolveViaServiceAccountRemoval(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	log := func(s string) {
		t.Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
	}

	c := newKubeClient(t)
	crc := newCRClient(t)
	csvName := genName("another-csv-")

	newNamespaceName := genName(testNamespace + "-")

	_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: newNamespaceName,
		},
	})
	require.NoError(t, err)
	defer func() {
		err = c.KubernetesInterface().CoreV1().Namespaces().Delete(newNamespaceName, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	log("Creating CRD")
	mainCRDPlural := genName("opgroup")
	mainCRD := newCRD(mainCRDPlural)
	cleanupCRD, err := createCRD(c, mainCRD)
	require.NoError(t, err)
	defer cleanupCRD()

	log("Creating operator group")
	serviceAccountName := genName("nginx-sa")
	// intentionally creating an operator group without a service account already existing
	operatorGroup := v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("e2e-operator-group-"),
			Namespace: newNamespaceName,
		},
		Spec: v1.OperatorGroupSpec{
			ServiceAccountName: serviceAccountName,
			TargetNamespaces:   []string{newNamespaceName},
		},
	}
	_, err = crc.OperatorsV1().OperatorGroups(newNamespaceName).Create(&operatorGroup)
	require.NoError(t, err)

	log("Creating CSV")

	// Create a new NamedInstallStrategy
	deploymentName := genName("operator-deployment")
	namedStrategy := newNginxInstallStrategy(deploymentName, nil, nil)

	aCSV := newCSV(csvName, newNamespaceName, "", semver.MustParse("0.0.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, namedStrategy)
	createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Create(&aCSV)
	require.NoError(t, err)

	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: newNamespaceName,
			Name:      serviceAccountName,
		},
	}
	ownerutil.AddNonBlockingOwner(serviceAccount, createdCSV)
	err = ownerutil.AddOwnerLabels(serviceAccount, createdCSV)
	require.NoError(t, err)

	_, err = c.CreateServiceAccount(serviceAccount)
	require.NoError(t, err)

	log("wait for CSV to fail")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Get(createdCSV.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
		return csvFailedChecker(fetched), nil
	})
	require.NoError(t, err)

	// now remove operator group specified service account
	createdOpGroup, err := crc.OperatorsV1().OperatorGroups(newNamespaceName).Get(operatorGroup.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	createdOpGroup.Spec.ServiceAccountName = ""
	_, err = crc.OperatorsV1().OperatorGroups(newNamespaceName).Update(createdOpGroup)
	require.NoError(t, err)

	log("wait for CSV to succeeed")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Get(createdCSV.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
		return csvSucceededChecker(fetched), nil
	})
	require.NoError(t, err)
}
