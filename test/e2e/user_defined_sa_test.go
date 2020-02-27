package e2e

import (
	"fmt"
	"testing"

	"github.com/blang/semver"
	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/apis/rbac"
)

func TestUserDefinedServiceAccountWithNoPermission(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, false)

	kubeclient := newKubeClient(t)
	crclient := newCRClient(t)

	namespace := genName("scoped-ns-")
	_, cleanupNS := newNamespace(t, kubeclient, namespace)
	defer cleanupNS()

	// Create a service account, but add no permission to it.
	saName := genName("scoped-sa-")
	_, cleanupSA := newServiceAccount(t, kubeclient, namespace, saName)
	defer cleanupSA()

	// Add an OperatorGroup and specify the service account.
	ogName := genName("scoped-og-")
	_, cleanupOG := newOperatorGroupWithServiceAccount(t, crclient, namespace, ogName, saName)
	defer cleanupOG()

	permissions := deploymentPermissions(t)
	catsrc, subSpec, catsrcCleanup := newCatalogSource(t, kubeclient, crclient, "scoped", namespace, permissions)
	defer catsrcCleanup()

	// Ensure that the catalog source is resolved before we create a subscription.
	_, err := fetchCatalogSource(t, crclient, catsrc.GetName(), namespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	subscriptionName := genName("scoped-sub-")
	cleanupSubscription := createSubscriptionForCatalog(t, crclient, namespace, subscriptionName, catsrc.GetName(), subSpec.Package, subSpec.Channel, subSpec.StartingCSV, subSpec.InstallPlanApproval)
	defer cleanupSubscription()

	// Wait until an install plan is created.
	subscription, err := fetchSubscription(t, crclient, namespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// We expect the InstallPlan to be in status: Failed.
	ipName := subscription.Status.Install.Name
	ipPhaseCheckerFunc := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed)
	ipGot, err := fetchInstallPlanWithNamespace(t, crclient, ipName, namespace, ipPhaseCheckerFunc)
	require.NoError(t, err)

	conditionGot := mustHaveCondition(t, ipGot, v1alpha1.InstallPlanInstalled)
	assert.Equal(t, corev1.ConditionFalse, conditionGot.Status)
	assert.Equal(t, v1alpha1.InstallPlanReasonComponentFailed, conditionGot.Reason)
	assert.Contains(t, conditionGot.Message, fmt.Sprintf("is forbidden: User \"system:serviceaccount:%s:%s\" cannot create resource", namespace, saName))

	// Verify that all step resources are in Unknown state.
	for _, step := range ipGot.Status.Plan {
		assert.Equal(t, v1alpha1.StepStatusUnknown, step.Status)
	}
}

func TestUserDefinedServiceAccountWithPermission(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, false)

	// Create the CatalogSource
	kubeclient := newKubeClient(t)
	crclient := newCRClient(t)

	namespace := genName("scoped-ns-")
	_, cleanupNS := newNamespace(t, kubeclient, namespace)
	defer cleanupNS()

	// Create a service account, add enough permission to it so that operator install is successful.
	saName := genName("scoped-sa")
	_, cleanupSA := newServiceAccount(t, kubeclient, namespace, saName)
	defer cleanupSA()
	cleanupPerm := grantPermission(t, kubeclient, namespace, saName)
	defer cleanupPerm()

	// Add an OperatorGroup and specify the service account.
	ogName := genName("scoped-og-")
	_, cleanupOG := newOperatorGroupWithServiceAccount(t, crclient, namespace, ogName, saName)
	defer cleanupOG()

	permissions := deploymentPermissions(t)
	catsrc, subSpec, catsrcCleanup := newCatalogSource(t, kubeclient, crclient, "scoped", namespace, permissions)
	defer catsrcCleanup()

	// Ensure that the catalog source is resolved before we create a subscription.
	_, err := fetchCatalogSource(t, crclient, catsrc.GetName(), namespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	subscriptionName := genName("scoped-sub-")
	cleanupSubscription := createSubscriptionForCatalog(t, crclient, namespace, subscriptionName, catsrc.GetName(), subSpec.Package, subSpec.Channel, subSpec.StartingCSV, subSpec.InstallPlanApproval)
	defer cleanupSubscription()

	// Wait until an install plan is created.
	subscription, err := fetchSubscription(t, crclient, namespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// We expect the InstallPlan to be in status: Complete.
	ipName := subscription.Status.Install.Name
	ipPhaseCheckerFunc := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete)
	ipGot, err := fetchInstallPlanWithNamespace(t, crclient, ipName, namespace, ipPhaseCheckerFunc)
	require.NoError(t, err)

	conditionGot := mustHaveCondition(t, ipGot, v1alpha1.InstallPlanInstalled)
	assert.Equal(t, v1alpha1.InstallPlanConditionReason(""), conditionGot.Reason)
	assert.Equal(t, corev1.ConditionTrue, conditionGot.Status)
	assert.Equal(t, "", conditionGot.Message)

	// Verify that all step resources are in Created state.
	for _, step := range ipGot.Status.Plan {
		assert.Equal(t, v1alpha1.StepStatusCreated, step.Status)
	}
}

func TestUserDefinedServiceAccountWithRetry(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, false)

	kubeclient := newKubeClient(t)
	crclient := newCRClient(t)

	namespace := genName("scoped-ns-")
	_, cleanupNS := newNamespace(t, kubeclient, namespace)
	defer cleanupNS()

	// Create a service account, but add no permission to it.
	saName := genName("scoped-sa-")
	_, cleanupSA := newServiceAccount(t, kubeclient, namespace, saName)
	defer cleanupSA()

	// Add an OperatorGroup and specify the service account.
	ogName := genName("scoped-og-")
	_, cleanupOG := newOperatorGroupWithServiceAccount(t, crclient, namespace, ogName, saName)
	defer cleanupOG()

	permissions := deploymentPermissions(t)
	catsrc, subSpec, catsrcCleanup := newCatalogSource(t, kubeclient, crclient, "scoped", namespace, permissions)
	defer catsrcCleanup()

	// Ensure that the catalog source is resolved before we create a subscription.
	_, err := fetchCatalogSource(t, crclient, catsrc.GetName(), namespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	subscriptionName := genName("scoped-sub-")
	cleanupSubscription := createSubscriptionForCatalog(t, crclient, namespace, subscriptionName, catsrc.GetName(), subSpec.Package, subSpec.Channel, subSpec.StartingCSV, subSpec.InstallPlanApproval)
	defer cleanupSubscription()

	// Wait until an install plan is created.
	subscription, err := fetchSubscription(t, crclient, namespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// We expect the InstallPlan to be in status: Failed.
	ipNameOld := subscription.Status.Install.Name
	ipPhaseCheckerFunc := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed)
	ipGotOld, err := fetchInstallPlanWithNamespace(t, crclient, ipNameOld, namespace, ipPhaseCheckerFunc)
	require.NoError(t, err)
	require.Equal(t, v1alpha1.InstallPlanPhaseFailed, ipGotOld.Status.Phase)

	// Grant permission now and this should trigger an retry of InstallPlan.
	cleanupPerm := grantPermission(t, kubeclient, namespace, saName)
	defer cleanupPerm()

	ipPhaseCheckerFunc = buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete)
	ipGotNew, err := fetchInstallPlanWithNamespace(t, crclient, ipNameOld, namespace, ipPhaseCheckerFunc)
	require.NoError(t, err)
	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, ipGotNew.Status.Phase)
}

func newNamespace(t *testing.T, client operatorclient.ClientInterface, name string) (ns *corev1.Namespace, cleanup cleanupFunc) {
	request := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	ns, err := client.KubernetesInterface().CoreV1().Namespaces().Create(request)
	require.NoError(t, err)
	require.NotNil(t, ns)

	cleanup = func() {
		err := client.KubernetesInterface().CoreV1().Namespaces().Delete(ns.GetName(), &metav1.DeleteOptions{})
		require.NoError(t, err)
	}

	return
}

func newServiceAccount(t *testing.T, client operatorclient.ClientInterface, namespace, name string) (sa *corev1.ServiceAccount, cleanup cleanupFunc) {
	request := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}

	sa, err := client.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Create(request)
	require.NoError(t, err)
	require.NotNil(t, sa)

	cleanup = func() {
		err := client.KubernetesInterface().CoreV1().ServiceAccounts(sa.GetNamespace()).Delete(sa.GetName(), &metav1.DeleteOptions{})
		require.NoError(t, err)
	}

	return
}

func newOperatorGroupWithServiceAccount(t *testing.T, client versioned.Interface, namespace, name, serviceAccountName string) (og *v1.OperatorGroup, cleanup cleanupFunc) {
	request := &v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1.OperatorGroupSpec{
			TargetNamespaces: []string{
				namespace,
			},
			ServiceAccountName: serviceAccountName,
		},
	}

	og, err := client.OperatorsV1().OperatorGroups(namespace).Create(request)
	require.NoError(t, err)
	require.NotNil(t, og)

	cleanup = func() {
		err := client.OperatorsV1().OperatorGroups(og.GetNamespace()).Delete(og.GetName(), &metav1.DeleteOptions{})
		require.NoError(t, err)
	}

	return
}

func newCatalogSource(t *testing.T, kubeclient operatorclient.ClientInterface, crclient versioned.Interface, prefix, namespace string, permissions []v1alpha1.StrategyDeploymentPermissions) (catsrc *v1alpha1.CatalogSource, subscriptionSpec *v1alpha1.SubscriptionSpec, cleanup cleanupFunc) {
	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"

	crd := apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
				Plural:   crdPlural,
				Singular: crdPlural,
				Kind:     crdPlural,
				ListKind: "list" + crdPlural,
			},
			Scope: "Namespaced",
		},
	}

	prefixFunc := func(s string) string {
		return fmt.Sprintf("%s-%s-", prefix, s)
	}

	// Create CSV
	packageName := genName(prefixFunc("package"))
	stableChannel := "stable"

	namedStrategy := newNginxInstallStrategy(genName(prefixFunc("dep")), permissions, nil)
	csvA := newCSV("nginx-a", namespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)
	csvB := newCSV("nginx-b", namespace, "nginx-a", semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

	// Create PackageManifests
	manifests := []registry.PackageManifest{
		{
			PackageName: packageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: csvB.GetName()},
			},
			DefaultChannelName: stableChannel,
		},
	}

	catalogSourceName := genName(prefixFunc("catsrc"))
	catsrc, cleanup = createInternalCatalogSource(t, kubeclient, crclient, catalogSourceName, namespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csvA, csvB})
	require.NotNil(t, catsrc)
	require.NotNil(t, cleanup)

	subscriptionSpec = &v1alpha1.SubscriptionSpec{
		CatalogSource:          catsrc.GetName(),
		CatalogSourceNamespace: catsrc.GetNamespace(),
		Package:                packageName,
		Channel:                stableChannel,
		StartingCSV:            csvB.GetName(),
		InstallPlanApproval:    v1alpha1.ApprovalAutomatic,
	}
	return
}

func newCatalogSourceWithDependencies(t *testing.T, kubeclient operatorclient.ClientInterface, crclient versioned.Interface, prefix, namespace string, permissions []v1alpha1.StrategyDeploymentPermissions) (catsrc *v1alpha1.CatalogSource, subscriptionSpec *v1alpha1.SubscriptionSpec, cleanup cleanupFunc) {
	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"

	crd := apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
				Plural:   crdPlural,
				Singular: crdPlural,
				Kind:     crdPlural,
				ListKind: "list" + crdPlural,
			},
			Scope: "Namespaced",
		},
	}

	prefixFunc := func(s string) string {
		return fmt.Sprintf("%s-%s-", prefix, s)
	}

	// Create CSV
	packageName1 := genName(prefixFunc("package"))
	packageName2 := genName(prefixFunc("package"))
	stableChannel := "stable"

	namedStrategy := newNginxInstallStrategy(genName(prefixFunc("dep")), permissions, nil)
	csvA := newCSV("nginx-req-dep", namespace, "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{crd}, namedStrategy)
	csvB := newCSV("nginx-dependency", namespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

	// Create PackageManifests
	manifests := []registry.PackageManifest{
		{
			PackageName: packageName1,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: csvA.GetName()},
			},
			DefaultChannelName: stableChannel,
		},
		{
			PackageName: packageName2,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: csvB.GetName()},
			},
			DefaultChannelName: stableChannel,
		},
	}

	catalogSourceName := genName(prefixFunc("catsrc"))
	catsrc, cleanup = createInternalCatalogSource(t, kubeclient, crclient, catalogSourceName, namespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csvA, csvB})
	require.NotNil(t, catsrc)
	require.NotNil(t, cleanup)

	subscriptionSpec = &v1alpha1.SubscriptionSpec{
		CatalogSource:          catsrc.GetName(),
		CatalogSourceNamespace: catsrc.GetNamespace(),
		Package:                packageName1,
		Channel:                stableChannel,
		StartingCSV:            csvA.GetName(),
		InstallPlanApproval:    v1alpha1.ApprovalAutomatic,
	}
	return
}

func mustHaveCondition(t *testing.T, ip *v1alpha1.InstallPlan, conditionType v1alpha1.InstallPlanConditionType) (condition *v1alpha1.InstallPlanCondition) {
	for i := range ip.Status.Conditions {
		if ip.Status.Conditions[i].Type == conditionType {
			condition = &ip.Status.Conditions[i]
			break
		}
	}

	require.NotNil(t, condition)
	return
}

func deploymentPermissions(t *testing.T) []v1alpha1.StrategyDeploymentPermissions {
	// Generate permissions
	serviceAccountName := genName("nginx-sa-")
	permissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: serviceAccountName,
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{rbac.VerbAll},
					APIGroups: []string{rbac.APIGroupAll},
					Resources: []string{rbac.ResourceAll}},
			},
		},
	}

	return permissions
}

func grantPermission(t *testing.T, client operatorclient.ClientInterface, namespace, serviceAccountName string) (cleanup cleanupFunc) {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("scoped-role-"),
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{rbac.VerbAll},
				APIGroups: []string{rbac.APIGroupAll},
				Resources: []string{rbac.ResourceAll},
			},
		},
	}

	role, err := client.KubernetesInterface().RbacV1().Roles(namespace).Create(role)
	require.NoError(t, err)

	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("scoped-rolebinding-"),
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      serviceAccountName,
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     role.GetName(),
		},
	}

	binding, err = client.KubernetesInterface().RbacV1().RoleBindings(namespace).Create(binding)
	require.NoError(t, err)

	cleanup = func() {
		err := client.KubernetesInterface().RbacV1().Roles(role.GetNamespace()).Delete(role.GetName(), &metav1.DeleteOptions{})
		require.NoError(t, err)

		err = client.KubernetesInterface().RbacV1().RoleBindings(binding.GetNamespace()).Delete(binding.GetName(), &metav1.DeleteOptions{})
		require.NoError(t, err)
	}

	return
}
