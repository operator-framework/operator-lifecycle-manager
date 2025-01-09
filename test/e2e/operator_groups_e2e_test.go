package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Operator Group", Label("OperatorGroup"), func() {
	var (
		c                  operatorclient.ClientInterface
		crc                versioned.Interface
		generatedNamespace corev1.Namespace
	)

	BeforeEach(func() {
		c = ctx.Ctx().KubeClient()
		crc = ctx.Ctx().OperatorClient()

		generatedNamespace = SetupGeneratedTestNamespace(genName("operator-group-e2e-"))

	})

	AfterEach(func() {
		TearDown(generatedNamespace.GetName())
	})

	It("e2e functionality", func() {

		By(`Create namespace with specific label`)
		By(`Create CRD`)
		By(`Create CSV in operator namespace`)
		By(`Create operator group that watches namespace and uses specific label`)
		By(`Verify operator group status contains correct status`)
		By(`Verify csv in target namespace exists, has copied status, has annotations`)
		By(`Verify deployments have correct namespace annotation`)
		By(`(Verify that the operator can operate in the target namespace)`)
		By(`Update CSV to support no InstallModes`)
		By(`Verify the CSV transitions to FAILED`)
		By(`Verify the copied CSV transitions to FAILED`)
		By(`Delete CSV`)
		By(`Verify copied CVS is deleted`)

		log := func(s string) {
			GinkgoT().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
		}

		csvName := genName("another-csv-") // must be lowercase for DNS-1123 validation

		opGroupNamespace := genName(generatedNamespace.GetName() + "-")
		matchingLabel := map[string]string{"inGroup": opGroupNamespace}
		otherNamespaceName := genName(opGroupNamespace + "-")
		bothNamespaceNames := opGroupNamespace + "," + otherNamespaceName

		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   opGroupNamespace,
				Labels: matchingLabel,
			},
		}, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err = c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), opGroupNamespace, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		otherNamespace := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   otherNamespaceName,
				Labels: matchingLabel,
			},
		}
		createdOtherNamespace, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &otherNamespace, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err = c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), otherNamespaceName, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		log("Creating CRD")
		mainCRDPlural := genName("opgroup")
		mainCRD := newCRD(mainCRDPlural)
		cleanupCRD, err := createCRD(c, mainCRD)
		require.NoError(GinkgoT(), err)
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
		_, err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Create(context.TODO(), &operatorGroup, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		By(`fetched namespaces might be in any order`)
		namespaces := map[string]bool{}
		namespaces[opGroupNamespace] = true
		namespaces[createdOtherNamespace.GetName()] = true

		log("Waiting on operator group to have correct status")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, fetchErr := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(context.TODO(), operatorGroup.Name, metav1.GetOptions{})
			if fetchErr != nil {
				log(fmt.Sprintf("error getting operatorgroup %s/%s: %v", opGroupNamespace, operatorGroup.Name, err))
				return false, nil
			}
			if len(namespaces) != len(fetched.Status.Namespaces) {
				log(fmt.Sprintf("element length mismatch: %v vs %v", namespaces, fetched.Status.Namespaces))
				return false, nil
			}
			for _, v := range fetched.Status.Namespaces {
				if !namespaces[v] {
					log(fmt.Sprintf("element values mismatch: %v vs %v", namespaces, fetched.Status.Namespaces))
					return false, nil
				}
			}
			return true, nil
		})
		require.NoError(GinkgoT(), err)

		log("Creating CSV")

		By(`Generate permissions`)
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

		By(`Create a new NamedInstallStrategy`)
		deploymentName := genName("operator-deployment")
		namedStrategy := newNginxInstallStrategy(deploymentName, permissions, nil)

		aCSV := newCSV(csvName, opGroupNamespace, "", semver.MustParse("0.0.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, nil, &namedStrategy)
		createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Create(context.TODO(), &aCSV, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: opGroupNamespace,
				Name:      serviceAccountName,
			},
		}
		ownerutil.AddNonBlockingOwner(serviceAccount, createdCSV)
		err = ownerutil.AddOwnerLabels(serviceAccount, createdCSV)
		require.NoError(GinkgoT(), err)

		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: opGroupNamespace,
				Name:      serviceAccountName + "-role",
			},
			Rules: permissions[0].Rules,
		}
		ownerutil.AddNonBlockingOwner(role, createdCSV)
		err = ownerutil.AddOwnerLabels(role, createdCSV)
		require.NoError(GinkgoT(), err)

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
		require.NoError(GinkgoT(), err)

		_, err = c.CreateServiceAccount(serviceAccount)
		require.NoError(GinkgoT(), err)
		_, err = c.CreateRole(role)
		require.NoError(GinkgoT(), err)
		_, err = c.CreateRoleBinding(roleBinding)
		require.NoError(GinkgoT(), err)

		log("wait for CSV to succeed")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(context.TODO(), createdCSV.GetName(), metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
			return csvSucceededChecker(fetched), nil
		})
		require.NoError(GinkgoT(), err)

		log("Waiting for operator namespace csv to have annotations")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				if apierrors.IsNotFound(fetchErr) {
					return false, nil
				}
				log(fmt.Sprintf("Error (in %v): %v", generatedNamespace.GetName(), fetchErr.Error()))
				return false, fetchErr
			}
			if checkOperatorGroupAnnotations(fetchedCSV, &operatorGroup, true, bothNamespaceNames) == nil {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)

		log("Waiting for target namespace csv to have annotations (but not target namespaces)")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				if apierrors.IsNotFound(fetchErr) {
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
			fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				if apierrors.IsNotFound(fetchErr) {
					return false, nil
				}
				GinkgoT().Logf("Error (in %v): %v", otherNamespaceName, fetchErr.Error())
				return false, fetchErr
			}
			if fetchedCSV.Status.Reason == v1alpha1.CSVReasonCopied {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)

		log("Waiting on deployment to have correct annotations")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			createdDeployment, err := c.GetDeployment(opGroupNamespace, deploymentName)
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			if checkOperatorGroupAnnotations(&createdDeployment.Spec.Template, &operatorGroup, true, bothNamespaceNames) == nil {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)

		By(`check rbac in target namespace`)
		informerFactory := informers.NewSharedInformerFactory(c.KubernetesInterface(), 1*time.Second)
		roleInformer := informerFactory.Rbac().V1().Roles()
		roleBindingInformer := informerFactory.Rbac().V1().RoleBindings()
		clusterRoleInformer := informerFactory.Rbac().V1().ClusterRoles()
		clusterRoleBindingInformer := informerFactory.Rbac().V1().ClusterRoleBindings()

		By(`kick off informers`)
		stopCh := make(chan struct{})
		defer func() {
			stopCh <- struct{}{}
		}()

		for _, informer := range []cache.SharedIndexInformer{roleInformer.Informer(), roleBindingInformer.Informer(), clusterRoleInformer.Informer(), clusterRoleBindingInformer.Informer()} {
			go func() {
				defer GinkgoRecover()
				informer.Run(stopCh)
			}()

			synced := func() (bool, error) {
				return informer.HasSynced(), nil
			}

			By(`wait until the informer has synced to continue`)
			err := wait.PollUntil(500*time.Millisecond, synced, stopCh)
			require.NoError(GinkgoT(), err)
		}

		ruleChecker := install.NewCSVRuleChecker(roleInformer.Lister(), roleBindingInformer.Lister(), clusterRoleInformer.Lister(), clusterRoleBindingInformer.Lister(), &aCSV)

		log("Waiting for operator to have rbac in target namespace")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			for _, perm := range permissions {
				sa, err := c.GetServiceAccount(opGroupNamespace, perm.ServiceAccountName)
				require.NoError(GinkgoT(), err)
				for _, rule := range perm.Rules {
					satisfied, err := ruleChecker.RuleSatisfied(sa, otherNamespaceName, rule)
					if err != nil {
						GinkgoT().Log(err.Error())
						return false, nil
					}
					if !satisfied {
						return false, nil
					}
				}
			}
			return true, nil
		})

		By(`validate provided API clusterroles for the operatorgroup`)
		existingClusterRoleList, err := c.KubernetesInterface().RbacV1().ClusterRoles().List(context.TODO(), metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(ownerutil.OwnerLabel(&operatorGroup, "OperatorGroup")).String(),
		})
		require.NoError(GinkgoT(), err)
		require.Len(GinkgoT(), existingClusterRoleList.Items, 3)

		for _, role := range existingClusterRoleList.Items {
			if strings.HasSuffix(role.Name, "admin") {
				adminPolicyRules := []rbacv1.PolicyRule{
					{Verbs: []string{"*"}, APIGroups: []string{mainCRD.Spec.Group}, Resources: []string{mainCRDPlural}},
				}
				if assert.Equal(GinkgoT(), adminPolicyRules, role.Rules) == false {
					fmt.Println(cmp.Diff(adminPolicyRules, role.Rules))
					GinkgoT().Fail()
				}

			} else if strings.HasSuffix(role.Name, "edit") {
				editPolicyRules := []rbacv1.PolicyRule{
					{Verbs: []string{"create", "update", "patch", "delete"}, APIGroups: []string{mainCRD.Spec.Group}, Resources: []string{mainCRDPlural}},
				}
				if assert.Equal(GinkgoT(), editPolicyRules, role.Rules) == false {
					fmt.Println(cmp.Diff(editPolicyRules, role.Rules))
					GinkgoT().Fail()
				}
			} else if strings.HasSuffix(role.Name, "view") {
				viewPolicyRules := []rbacv1.PolicyRule{
					{Verbs: []string{"get"}, APIGroups: []string{"apiextensions.k8s.io"}, Resources: []string{"customresourcedefinitions"}, ResourceNames: []string{mainCRD.Name}},
					{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{mainCRD.Spec.Group}, Resources: []string{mainCRDPlural}},
				}
				if assert.Equal(GinkgoT(), viewPolicyRules, role.Rules) == false {
					fmt.Println(cmp.Diff(viewPolicyRules, role.Rules))
					GinkgoT().Fail()
				}
			}
		}

		By(`Unsupport all InstallModes`)
		log("unsupporting all csv installmodes")
		fetchedCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(context.TODO(), csvName, metav1.GetOptions{})
		require.NoError(GinkgoT(), err, "could not fetch csv")
		fetchedCSV.Spec.InstallModes = []v1alpha1.InstallMode{}
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(fetchedCSV.GetNamespace()).Update(context.TODO(), fetchedCSV, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err, "could not update csv installmodes")

		By(`Ensure CSV fails`)
		_, err = fetchCSV(crc, opGroupNamespace, csvName, csvFailedChecker)
		require.NoError(GinkgoT(), err, "csv did not transition to failed as expected")

		By(`ensure deletion cleans up copied CSV`)
		log("deleting parent csv")
		err = crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Delete(context.TODO(), csvName, metav1.DeleteOptions{})
		require.NoError(GinkgoT(), err)

		log("waiting for orphaned csv to be deleted")
		err = waitForDelete(func() error {
			_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(context.TODO(), csvName, metav1.GetOptions{})
			return err
		})
		require.NoError(GinkgoT(), err)

		err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Delete(context.TODO(), operatorGroup.Name, metav1.DeleteOptions{})
		require.NoError(GinkgoT(), err)
		GinkgoT().Log("Waiting for OperatorGroup RBAC to be garbage collected")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			_, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(context.TODO(), operatorGroup.Name+"-admin", metav1.GetOptions{})
			if err == nil {
				return false, nil
			}
			return true, err
		})
		require.True(GinkgoT(), apierrors.IsNotFound(err))

		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			_, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(context.TODO(), operatorGroup.Name+"-edit", metav1.GetOptions{})
			if err == nil {
				return false, nil
			}
			return true, err
		})
		require.True(GinkgoT(), apierrors.IsNotFound(err))

		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			_, err := c.KubernetesInterface().RbacV1().ClusterRoles().Get(context.TODO(), operatorGroup.Name+"-view", metav1.GetOptions{})
			if err == nil {
				return false, nil
			}
			return true, err
		})
		require.True(GinkgoT(), apierrors.IsNotFound(err))
	})
	It("role aggregation", func() {

		By(`kubectl -n a8v4sw  auth can-i create alp999.cluster.com --as system:serviceaccount:a8v4sw:padmin-xqdfz`)

		By(`Generate namespaceA`)
		By(`Generate operatorGroupA - OwnNamespace`)
		By(`Generate csvA in namespaceA with all installmodes supported`)
		By(`Create crd so csv succeeds`)
		By(`Ensure clusterroles created and aggregated for access provided APIs`)

		nsA := genName("a")
		GinkgoT().Logf("generating namespaceA: %s", nsA)
		c := newKubeClient()
		for _, ns := range []string{nsA} {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			}
			_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
			require.NoError(GinkgoT(), err)
			defer func(name string) {
				require.NoError(GinkgoT(), c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), name, metav1.DeleteOptions{}))
			}(ns)
		}

		groupAName := genName("a")
		GinkgoT().Logf("Generate operatorGroupA - OwnNamespace: %s", groupAName)
		groupA := newOperatorGroup(nsA, groupAName, nil, nil, []string{nsA}, false)
		_, err := crc.OperatorsV1().OperatorGroups(nsA).Create(context.TODO(), groupA, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1().OperatorGroups(nsA).Delete(context.TODO(), groupA.GetName(), metav1.DeleteOptions{}))
		}()

		crdAName := genName("a")
		strategyName := genName("dep-")
		csvAName := "nginx-a"
		GinkgoT().Logf("Generate csv (%s/%s) with crd %s and with all installmodes supported: %s", nsA, csvAName, crdAName, strategyName)
		crd := newCRD(crdAName)
		namedStrategy := newNginxInstallStrategy(strategyName, nil, nil)
		csvA := newCSV(csvAName, nsA, "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, &namedStrategy)
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Create(context.TODO(), &csvA, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Delete(context.TODO(), csvA.GetName(), metav1.DeleteOptions{}))
		}()

		GinkgoT().Logf("Create crd %s so csv %s/%s succeeds", crdAName, nsA, csvAName)
		cleanupCRD, err := createCRD(c, crd)
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		_, err = fetchCSV(crc, nsA, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		depName := genName("hat-server")
		GinkgoT().Logf("Create csv %s/%s for an apiserver", nsA, depName)
		mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
		version := "v1alpha1"
		mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
		mockKinds := []string{"fez", "fedora"}
		mockNames := []string{"fezs", "fedoras"}
		depSpec := newMockExtServerDeployment(depName, []mockGroupVersionKind{{depName, mockGroupVersion, mockKinds, 5443}})
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

		GinkgoT().Logf("Create the APIService CSV %s/%s", nsA, depName)
		cleanupCSV, err := createCSV(c, crc, csvB, nsA, false, true)
		require.NoError(GinkgoT(), err)
		defer cleanupCSV()

		GinkgoT().Logf("Fetch the APIService CSV %s/%s", nsA, depName)
		_, err = fetchCSV(crc, nsA, csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		GinkgoT().Logf("Ensure clusterroles created and aggregated for access provided APIs")
		padmin, cleanupPadmin := createProjectAdmin(GinkgoT(), c, nsA)
		defer cleanupPadmin()

		GinkgoT().Logf("Check CRD access aggregated")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(context.TODO(), &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: padmin,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Namespace: nsA,
						Group:     crd.Spec.Group,
						Version:   crd.Spec.Versions[0].Name,
						Resource:  crd.Spec.Names.Plural,
						Verb:      "create",
					},
				},
			}, metav1.CreateOptions{})
			if err != nil {
				return false, err
			}
			if res == nil {
				return false, nil
			}
			GinkgoT().Logf("checking padmin for permission: %#v", res)
			return res.Status.Allowed, nil
		})
		require.NoError(GinkgoT(), err)

		GinkgoT().Logf("Check apiserver access aggregated")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(context.TODO(), &authorizationv1.SubjectAccessReview{
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
			}, metav1.CreateOptions{})
			if err != nil {
				return false, err
			}
			if res == nil {
				return false, nil
			}
			GinkgoT().Logf("checking padmin for permission: %#v", res)
			return res.Status.Allowed, nil
		})
		require.NoError(GinkgoT(), err)
	})
	It("install mode support", func() {

		By(`Generate namespaceA`)
		By(`Generate namespaceB`)
		By(`Create operatorGroupA in namespaceA that selects namespaceA`)
		By(`Generate csvA with an unfulfilled required CRD and no supported InstallModes in namespaceA`)
		By(`Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"`)
		By(`Update csvA to have OwnNamespace supported=true`)
		By(`Ensure csvA transitions to Succeeded`)
		By(`Update operatorGroupA's target namespaces to select namespaceB`)
		By(`Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"`)
		By(`Update csvA to have SingleNamespace supported=true`)
		By(`Ensure csvA transitions to Pending`)
		By(`Update operatorGroupA's target namespaces to select namespaceA and namespaceB`)
		By(`Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"`)
		By(`Update csvA to have MultiNamespace supported=true`)
		By(`Ensure csvA transitions to Pending`)
		By(`Update operatorGroupA to select all namespaces`)
		By(`Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"`)
		By(`Update csvA to have AllNamespaces supported=true`)
		By(`Ensure csvA transitions to Pending`)

		By(`Generate namespaceA and namespaceB`)
		nsA := genName("a")
		nsB := genName("b")

		c := newKubeClient()
		for _, ns := range []string{nsA, nsB} {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			}
			_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
			require.NoError(GinkgoT(), err)
			defer func(name string) {
				require.NoError(GinkgoT(), c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), name, metav1.DeleteOptions{}))
			}(ns)
		}

		By(`Generate operatorGroupA`)
		groupA := newOperatorGroup(nsA, genName("a"), nil, nil, []string{nsA}, false)
		_, err := crc.OperatorsV1().OperatorGroups(nsA).Create(context.TODO(), groupA, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1().OperatorGroups(nsA).Delete(context.TODO(), groupA.GetName(), metav1.DeleteOptions{}))
		}()

		By(`Generate csvA in namespaceA with no supported InstallModes`)
		crd := newCRD(genName("b"))
		namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
		csv := newCSV("nginx-a", nsA, "", semver.MustParse("0.1.0"), nil, []apiextensionsv1.CustomResourceDefinition{crd}, &namedStrategy)
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
		csvA, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Create(context.TODO(), csvA, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Delete(context.TODO(), csvA.GetName(), metav1.DeleteOptions{}))
		}()

		By(`Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"`)
		failedWithUnsupportedOperatorGroup := func(csv *v1alpha1.ClusterServiceVersion) bool {
			return csvFailedChecker(csv) && csv.Status.Reason == v1alpha1.CSVReasonUnsupportedOperatorGroup
		}
		csvA, err = fetchCSV(crc, nsA, csvA.GetName(), failedWithUnsupportedOperatorGroup)
		require.NoError(GinkgoT(), err)

		By(`Update csvA to have OwnNamespace supported=true`)
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
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(context.TODO(), csvA, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Create crd so csv succeeds`)
		cleanupCRD, err := createCRD(c, crd)
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		By(`Ensure csvA transitions to Succeeded`)
		csvA, err = fetchCSV(crc, nsA, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Update operatorGroupA's target namespaces to select namespaceB`)
		groupA, err = crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
		require.NoError(GinkgoT(), err)
		groupA.Spec.TargetNamespaces = []string{nsB}
		_, err = crc.OperatorsV1().OperatorGroups(nsA).Update(context.TODO(), groupA, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"`)
		csvA, err = fetchCSV(crc, nsA, csvA.GetName(), failedWithUnsupportedOperatorGroup)
		require.NoError(GinkgoT(), err)

		By(`Update csvA to have SingleNamespace supported=true`)
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
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(context.TODO(), csvA, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Ensure csvA transitions to Succeeded`)
		csvA, err = fetchCSV(crc, nsA, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Update operatorGroupA's target namespaces to select namespaceA and namespaceB`)
		groupA, err = crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
		require.NoError(GinkgoT(), err)
		groupA.Spec.TargetNamespaces = []string{nsA, nsB}
		_, err = crc.OperatorsV1().OperatorGroups(nsA).Update(context.TODO(), groupA, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"`)
		csvA, err = fetchCSV(crc, nsA, csvA.GetName(), failedWithUnsupportedOperatorGroup)
		require.NoError(GinkgoT(), err)

		By(`Update csvA to have MultiNamespace supported=true`)
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
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(context.TODO(), csvA, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Ensure csvA transitions to Succeeded`)
		csvA, err = fetchCSV(crc, nsA, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Update operatorGroupA's target namespaces to select all namespaces`)
		groupA, err = crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
		require.NoError(GinkgoT(), err)
		groupA.Spec.TargetNamespaces = []string{}
		_, err = crc.OperatorsV1().OperatorGroups(nsA).Update(context.TODO(), groupA, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Ensure csvA transitions to Failed with reason "UnsupportedOperatorGroup"`)
		csvA, err = fetchCSV(crc, nsA, csvA.GetName(), failedWithUnsupportedOperatorGroup)
		require.NoError(GinkgoT(), err)

		By(`Update csvA to have AllNamespaces supported=true`)
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
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(context.TODO(), csvA, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Ensure csvA transitions to Pending`)
		csvA, err = fetchCSV(crc, nsA, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)
	})
	It("[FLAKE] intersection", func() {

		By(`Generate namespaceA`)
		By(`Generate namespaceB`)
		By(`Generate namespaceC`)
		By(`Generate namespaceD`)
		By(`Generate namespaceE`)
		By(`Generate operatorGroupD in namespaceD that selects namespace D and E`)
		By(`Generate csvD in namespaceD`)
		By(`Wait for csvD to be successful`)
		By(`Wait for csvD to have a CSV with copied status in namespace E`)
		By(`Wait for operatorGroupD to have providedAPI annotation with crdD's Kind.version.group`)
		By(`Generate operatorGroupA in namespaceA that selects AllNamespaces`)
		By(`Generate csvD in namespaceA`)
		By(`Wait for csvD to fail with status "InterOperatorGroupOwnerConflict"`)
		By(`Ensure operatorGroupA's providedAPIs are empty`)
		By(`Ensure csvD in namespaceD is still successful`)
		By(`Generate csvA in namespaceA that owns crdA`)
		By(`Wait for csvA to be successful`)
		By(`Ensure clusterroles created and aggregated for accessing provided APIs`)
		By(`Wait for operatorGroupA to have providedAPI annotation with crdA's Kind.version.group in its providedAPIs annotation`)
		By(`Wait for csvA to have a CSV with copied status in namespace D`)
		By(`Ensure csvA retains the operatorgroup annotations for operatorgroupA`)
		By(`Wait for csvA to have a CSV with copied status in namespace C`)
		By(`Generate operatorGroupB in namespaceB that selects namespace C`)
		By(`Generate csvB in namespaceB that owns crdA`)
		By(`Wait for csvB to fail with status "InterOperatorGroupOwnerConflict"`)
		By(`Delete csvA`)
		By(`Wait for crdA's Kind.version.group to be removed from operatorGroupA's providedAPIs annotation`)
		By(`Ensure csvA's deployments are deleted`)
		By(`Wait for csvB to be successful`)
		By(`Wait for operatorGroupB to have providedAPI annotation with crdB's Kind.version.group`)
		By(`Wait for csvB to have a CSV with a copied status in namespace C`)

		By(`Create a catalog for csvA, csvB, and csvD`)
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
		kvgA := fmt.Sprintf("%s.%s.%s", crdA.Spec.Names.Kind, crdA.Spec.Versions[0].Name, crdA.Spec.Group)
		kvgB := fmt.Sprintf("%s.%s.%s", crdB.Spec.Names.Kind, crdB.Spec.Versions[0].Name, crdB.Spec.Group)
		kvgD := fmt.Sprintf("%s.%s.%s", crdD.Spec.Names.Kind, crdD.Spec.Versions[0].Name, crdD.Spec.Group)
		csvA := newCSV(pkgAStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crdA}, nil, &strategyA)
		csvB := newCSV(pkgBStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crdA, crdB}, nil, &strategyB)
		csvD := newCSV(pkgDStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crdD}, nil, &strategyD)

		By(`Create namespaces`)
		nsA, nsB, nsC, nsD, nsE := genName("a-"), genName("b-"), genName("c-"), genName("d-"), genName("e-")
		for _, ns := range []string{nsA, nsB, nsC, nsD, nsE} {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			}
			_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
			require.NoError(GinkgoT(), err)
			defer func(name string) {
				require.NoError(GinkgoT(), c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), name, metav1.DeleteOptions{}))
			}(ns)
		}

		By(`Create the initial catalogsources`)
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
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalog, nsA, manifests, []apiextensionsv1.CustomResourceDefinition{crdA, crdD, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB, csvD})
		defer cleanupCatalogSource()
		_, err := fetchCatalogSourceOnStatus(crc, catalog, nsA, catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)
		_, cleanupCatalogSource = createInternalCatalogSource(c, crc, catalog, nsB, manifests, []apiextensionsv1.CustomResourceDefinition{crdA, crdD, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB, csvD})
		defer cleanupCatalogSource()
		_, err = fetchCatalogSourceOnStatus(crc, catalog, nsB, catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)
		_, cleanupCatalogSource = createInternalCatalogSource(c, crc, catalog, nsD, manifests, []apiextensionsv1.CustomResourceDefinition{crdA, crdD, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB, csvD})
		defer cleanupCatalogSource()
		_, err = fetchCatalogSourceOnStatus(crc, catalog, nsD, catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By(`Create operatorgroups`)
		groupA := newOperatorGroup(nsA, genName("a-"), nil, nil, nil, false)
		groupB := newOperatorGroup(nsB, genName("b-"), nil, nil, []string{nsC}, false)
		groupD := newOperatorGroup(nsD, genName("d-"), nil, nil, []string{nsD, nsE}, false)
		for _, group := range []*v1.OperatorGroup{groupA, groupB, groupD} {
			_, err := crc.OperatorsV1().OperatorGroups(group.GetNamespace()).Create(context.TODO(), group, metav1.CreateOptions{})
			require.NoError(GinkgoT(), err)
			defer func(namespace, name string) {
				require.NoError(GinkgoT(), crc.OperatorsV1().OperatorGroups(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{}))
			}(group.GetNamespace(), group.GetName())
		}

		By(`Create subscription for csvD in namespaceD`)
		subDName := genName("d-")
		cleanupSubD := createSubscriptionForCatalog(crc, nsD, subDName, catalog, pkgD, stableChannel, pkgDStable, v1alpha1.ApprovalAutomatic)
		defer cleanupSubD()
		subD, err := fetchSubscription(crc, nsD, subDName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subD)

		By(`Await csvD's success`)
		_, err = fetchCSV(crc, nsD, csvD.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Await csvD's copy in namespaceE`)
		_, err = fetchCSV(crc, nsE, csvD.GetName(), csvCopiedChecker)
		require.NoError(GinkgoT(), err)

		By(`Await annotation on groupD`)
		q := func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsD).Get(context.TODO(), groupD.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgD}))

		By(`Create subscription for csvD2 in namespaceA`)
		subD2Name := genName("d2-")
		cleanupSubD2 := createSubscriptionForCatalog(crc, nsA, subD2Name, catalog, pkgD, stableChannel, pkgDStable, v1alpha1.ApprovalAutomatic)
		defer cleanupSubD2()
		subD2, err := fetchSubscription(crc, nsA, subD2Name, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subD2)

		By(`Await csvD2's failure`)
		csvD2, err := fetchCSV(crc, nsA, csvD.GetName(), csvFailedChecker)
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, csvD2.Status.Reason)

		By(`Ensure groupA's annotations are blank`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{}))

		By(`Ensure csvD is still successful`)
		_, err = fetchCSV(crc, nsD, csvD.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Create subscription for csvA in namespaceA`)
		subAName := genName("a-")
		cleanupSubA := createSubscriptionForCatalog(crc, nsA, subAName, catalog, pkgA, stableChannel, pkgAStable, v1alpha1.ApprovalAutomatic)
		defer cleanupSubA()
		subA, err := fetchSubscription(crc, nsA, subAName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subA)

		By(`Await csvA's success`)
		_, err = fetchCSV(crc, nsA, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Ensure clusterroles created and aggregated for access provided APIs`)
		padmin, cleanupPadmin := createProjectAdmin(GinkgoT(), c, nsA)
		defer cleanupPadmin()

		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(context.TODO(), &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: padmin,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Namespace: nsA,
						Group:     crdA.Spec.Group,
						Version:   crdA.Spec.Versions[0].Name,
						Resource:  crdA.Spec.Names.Plural,
						Verb:      "create",
					},
				},
			}, metav1.CreateOptions{})
			if err != nil {
				return false, err
			}
			if res == nil {
				return false, nil
			}
			GinkgoT().Log("checking padmin for permission")
			return res.Status.Allowed, nil
		})
		require.NoError(GinkgoT(), err)

		By(`Await annotation on groupA`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

		By(`Wait for csvA to have a CSV with copied status in namespace D`)
		csvAinNsD, err := fetchCSV(crc, nsD, csvA.GetName(), csvCopiedChecker)
		require.NoError(GinkgoT(), err)

		By(`trigger a resync of operatorgropuD`)
		fetchedGroupD, err := crc.OperatorsV1().OperatorGroups(nsD).Get(context.TODO(), groupD.GetName(), metav1.GetOptions{})
		require.NoError(GinkgoT(), err)

		fetchedGroupD.Annotations["bump"] = "update"
		_, err = crc.OperatorsV1().OperatorGroups(nsD).Update(context.TODO(), fetchedGroupD, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Ensure csvA retains the operatorgroup annotations for operatorgroupA`)
		csvAinNsD, err = fetchCSV(crc, nsD, csvA.GetName(), csvCopiedChecker)
		require.NoError(GinkgoT(), err)

		require.Equal(GinkgoT(), groupA.GetName(), csvAinNsD.Annotations[v1.OperatorGroupAnnotationKey])
		require.Equal(GinkgoT(), nsA, csvAinNsD.Annotations[v1.OperatorGroupNamespaceAnnotationKey])
		require.Equal(GinkgoT(), nsA, csvAinNsD.Labels[v1alpha1.CopiedLabelKey])

		By(`Await csvA's copy in namespaceC`)
		_, err = fetchCSV(crc, nsC, csvA.GetName(), csvCopiedChecker)
		require.NoError(GinkgoT(), err)

		By(`Create subscription for csvB in namespaceB`)
		subBName := genName("b-")
		cleanupSubB := createSubscriptionForCatalog(crc, nsB, subBName, catalog, pkgB, stableChannel, pkgBStable, v1alpha1.ApprovalAutomatic)
		defer cleanupSubB()
		subB, err := fetchSubscription(crc, nsB, subBName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subB)

		By(`Await csvB's failure`)
		fetchedB, err := fetchCSV(crc, nsB, csvB.GetName(), csvFailedChecker)
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, fetchedB.Status.Reason)

		By(`Ensure no annotation on groupB`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsB).Get(context.TODO(), groupB.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{}))

		By(`Delete csvA`)
		require.NoError(GinkgoT(), crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Delete(context.TODO(), csvA.GetName(), metav1.DeleteOptions{}))

		By(`Ensure annotations are removed from groupA`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: ""}))

		By(`Ensure csvA's deployment is deleted`)
		require.NoError(GinkgoT(), waitForDeploymentToDelete(generatedNamespace.GetName(), pkgAStable, c))

		By(`Await csvB's success`)
		_, err = fetchCSV(crc, nsB, csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Await csvB's copy in namespace C`)
		_, err = fetchCSV(crc, nsC, csvB.GetName(), csvCopiedChecker)
		require.NoError(GinkgoT(), err)

		By(`Ensure annotations exist on group B`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsB).Get(context.TODO(), groupB.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: strings.Join([]string{kvgA, kvgB}, ",")}))
	})
	It("static provider", func() {

		By(`Generate namespaceA`)
		By(`Generate namespaceB`)
		By(`Generate namespaceC`)
		By(`Generate namespaceD`)
		By(`Create static operatorGroupA in namespaceA that targets namespaceD with providedAPIs annotation containing KindA.version.group`)
		By(`Create operatorGroupB in namespaceB that targets all namespaces`)
		By(`Create operatorGroupC in namespaceC that targets namespaceC`)
		By(`Create csvA in namespaceB that provides KindA.version.group`)
		By(`Wait for csvA in namespaceB to fail`)
		By(`Ensure no providedAPI annotations on operatorGroupB`)
		By(`Ensure providedAPI annotations are unchanged on operatorGroupA`)
		By(`Create csvA in namespaceC`)
		By(`Wait for csvA in namespaceC to succeed`)
		By(`Ensure KindA.version.group providedAPI annotation on operatorGroupC`)
		By(`Create csvB in namespaceB that provides KindB.version.group`)
		By(`Wait for csvB to succeed`)
		By(`Wait for csvB to be copied to namespaceA, namespaceC, and namespaceD`)
		By(`Wait for KindB.version.group to exist in operatorGroupB's providedAPIs annotation`)
		By(`Add namespaceD to operatorGroupC's targetNamespaces`)
		By(`Wait for csvA in namespaceC to FAIL with status "InterOperatorGroupOwnerConflict"`)
		By(`Wait for KindA.version.group providedAPI annotation to be removed from operatorGroupC's providedAPIs annotation`)
		By(`Ensure KindA.version.group providedAPI annotation on operatorGroupA`)

		By(`Create a catalog for csvA, csvB`)
		pkgA := genName("a-")
		pkgB := genName("b-")
		pkgAStable := pkgA + "-stable"
		pkgBStable := pkgB + "-stable"
		stableChannel := "stable"
		strategyA := newNginxInstallStrategy(pkgAStable, nil, nil)
		strategyB := newNginxInstallStrategy(pkgBStable, nil, nil)
		crdA := newCRD(genName(pkgA))
		crdB := newCRD(genName(pkgB))
		kvgA := fmt.Sprintf("%s.%s.%s", crdA.Spec.Names.Kind, crdA.Spec.Versions[0].Name, crdA.Spec.Group)
		kvgB := fmt.Sprintf("%s.%s.%s", crdB.Spec.Names.Kind, crdB.Spec.Versions[0].Name, crdB.Spec.Group)
		csvA := newCSV(pkgAStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crdA}, nil, &strategyA)
		csvB := newCSV(pkgBStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crdB}, nil, &strategyB)

		By(`Create namespaces`)
		nsA, nsB, nsC, nsD := genName("a-"), genName("b-"), genName("c-"), genName("d-")

		for _, ns := range []string{nsA, nsB, nsC, nsD} {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			}
			_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
			require.NoError(GinkgoT(), err)
			defer func(name string) {
				require.NoError(GinkgoT(), c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), name, metav1.DeleteOptions{}))
			}(ns)
		}

		By(`Create the initial catalogsources`)
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

		By(`Create catalog in namespaceB and namespaceC`)
		catalog := genName("catalog-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalog, nsB, manifests, []apiextensionsv1.CustomResourceDefinition{crdA, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB})
		defer cleanupCatalogSource()
		_, err := fetchCatalogSourceOnStatus(crc, catalog, nsB, catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)
		_, cleanupCatalogSource = createInternalCatalogSource(c, crc, catalog, nsC, manifests, []apiextensionsv1.CustomResourceDefinition{crdA, crdB}, []v1alpha1.ClusterServiceVersion{csvA, csvB})
		defer cleanupCatalogSource()
		_, err = fetchCatalogSourceOnStatus(crc, catalog, nsC, catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By(`Create OperatorGroups`)
		groupA := newOperatorGroup(nsA, genName("a-"), map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}, nil, []string{nsD}, true)
		groupB := newOperatorGroup(nsB, genName("b-"), nil, nil, nil, false)
		groupC := newOperatorGroup(nsC, genName("d-"), nil, nil, []string{nsC}, false)
		for _, group := range []*v1.OperatorGroup{groupA, groupB, groupC} {
			_, err := crc.OperatorsV1().OperatorGroups(group.GetNamespace()).Create(context.TODO(), group, metav1.CreateOptions{})
			require.NoError(GinkgoT(), err)
			defer func(namespace, name string) {
				require.NoError(GinkgoT(), crc.OperatorsV1().OperatorGroups(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{}))
			}(group.GetNamespace(), group.GetName())
		}

		By(`Create subscription for csvA in namespaceB`)
		subAName := genName("a-")
		cleanupSubA := createSubscriptionForCatalog(crc, nsB, subAName, catalog, pkgA, stableChannel, pkgAStable, v1alpha1.ApprovalAutomatic)
		defer cleanupSubA()
		subA, err := fetchSubscription(crc, nsB, subAName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subA)

		By(`Await csvA's failure`)
		fetchedCSVA, err := fetchCSV(crc, nsB, csvA.GetName(), csvFailedChecker)
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, fetchedCSVA.Status.Reason)

		By(`Ensure operatorGroupB doesn't have providedAPI annotation`)
		q := func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsB).Get(context.TODO(), groupB.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{}))

		By(`Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

		By(`Create subscription for csvA in namespaceC`)
		cleanupSubAC := createSubscriptionForCatalog(crc, nsC, subAName, catalog, pkgA, stableChannel, pkgAStable, v1alpha1.ApprovalAutomatic)
		defer cleanupSubAC()
		subAC, err := fetchSubscription(crc, nsC, subAName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subAC)

		By(`Await csvA's success`)
		_, err = fetchCSV(crc, nsC, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Ensure operatorGroupC has KindA.version.group in its providedAPIs annotation`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsC).Get(context.TODO(), groupC.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

		By(`Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

		By(`Create subscription for csvB in namespaceB`)
		subBName := genName("b-")
		cleanupSubB := createSubscriptionForCatalog(crc, nsB, subBName, catalog, pkgB, stableChannel, pkgBStable, v1alpha1.ApprovalAutomatic)
		defer cleanupSubB()
		subB, err := fetchSubscription(crc, nsB, subBName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subB)

		By(`Await csvB's success`)
		_, err = fetchCSV(crc, nsB, csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Await copied csvBs`)
		_, err = fetchCSV(crc, nsA, csvB.GetName(), csvCopiedChecker)
		require.NoError(GinkgoT(), err)
		_, err = fetchCSV(crc, nsC, csvB.GetName(), csvCopiedChecker)
		require.NoError(GinkgoT(), err)
		_, err = fetchCSV(crc, nsD, csvB.GetName(), csvCopiedChecker)
		require.NoError(GinkgoT(), err)

		By(`Ensure operatorGroupB has KindB.version.group in its providedAPIs annotation`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsB).Get(context.TODO(), groupB.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgB}))

		By(`Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))

		By(`Add namespaceD to operatorGroupC's targetNamespaces`)
		groupC, err = crc.OperatorsV1().OperatorGroups(groupC.GetNamespace()).Get(context.TODO(), groupC.GetName(), metav1.GetOptions{})
		require.NoError(GinkgoT(), err)
		groupC.Spec.TargetNamespaces = []string{nsC, nsD}
		_, err = crc.OperatorsV1().OperatorGroups(groupC.GetNamespace()).Update(context.TODO(), groupC, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Wait for csvA in namespaceC to fail with status "InterOperatorGroupOwnerConflict"`)
		fetchedCSVA, err = fetchCSV(crc, nsC, csvA.GetName(), csvFailedChecker)
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, fetchedCSVA.Status.Reason)

		By(`Wait for crdA's providedAPIs to be removed from operatorGroupC's providedAPIs annotation`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsC).Get(context.TODO(), groupC.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: ""}))

		By(`Ensure operatorGroupA still has KindA.version.group in its providedAPIs annotation`)
		q = func() (metav1.ObjectMeta, error) {
			g, err := crc.OperatorsV1().OperatorGroups(nsA).Get(context.TODO(), groupA.GetName(), metav1.GetOptions{})
			return g.ObjectMeta, err
		}
		require.NoError(GinkgoT(), awaitAnnotations(GinkgoT(), q, map[string]string{v1.OperatorGroupProvidedAPIsAnnotationKey: kvgA}))
	})

	// TODO: Test OperatorGroup resizing collisions
	// TODO: Test Subscriptions with depedencies and transitive dependencies in intersecting OperatorGroups
	// TODO: Test Subscription upgrade paths with + and - providedAPIs
	It("CSV copy watching all namespaces", func() {

		csvName := genName("another-csv-") // must be lowercase for DNS-1123 validation

		opGroupNamespace := generatedNamespace.GetName()
		matchingLabel := map[string]string{"inGroup": opGroupNamespace}
		otherNamespaceName := genName(opGroupNamespace + "-")
		GinkgoT().Log("Creating CRD")
		mainCRDPlural := genName("opgroup-")
		mainCRD := newCRD(mainCRDPlural)
		cleanupCRD, err := createCRD(c, mainCRD)
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()
		operatorGroup, err := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(context.TODO(), fmt.Sprintf("%v-operatorgroup", opGroupNamespace), metav1.GetOptions{})
		require.NoError(GinkgoT(), err)

		expectedOperatorGroupStatus := v1.OperatorGroupStatus{
			Namespaces: []string{metav1.NamespaceAll},
		}
		GinkgoT().Log("Waiting on operator group to have correct status")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, fetchErr := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(context.TODO(), operatorGroup.Name, metav1.GetOptions{})
			if fetchErr != nil {
				return false, fetchErr
			}
			if len(fetched.Status.Namespaces) > 0 {
				require.ElementsMatch(GinkgoT(), expectedOperatorGroupStatus.Namespaces, fetched.Status.Namespaces)
				fmt.Println(fetched.Status.Namespaces)
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)
		GinkgoT().Log("Creating CSV")
		By(`Generate permissions`)
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
		serviceAccount, err = c.CreateServiceAccount(serviceAccount)
		require.NoError(GinkgoT(), err)
		defer func() {
			if env := os.Getenv("SKIP_CLEANUP"); env != "" {
				fmt.Printf("Skipping cleanup of serviceaccount %s/%s...\n", serviceAccount.GetNamespace(), serviceAccount.GetName())
				return
			}
			c.DeleteServiceAccount(serviceAccount.GetNamespace(), serviceAccount.GetName(), metav1.NewDeleteOptions(0))
		}()
		createdRole, err := c.CreateRole(role)
		require.NoError(GinkgoT(), err)
		defer func() {
			if env := os.Getenv("SKIP_CLEANUP"); env != "" {
				fmt.Printf("Skipping cleanup of role %s/%s...\n", role.GetNamespace(), role.GetName())
				return
			}
			c.DeleteRole(role.GetNamespace(), role.GetName(), metav1.NewDeleteOptions(0))
		}()
		createdRoleBinding, err := c.CreateRoleBinding(roleBinding)
		require.NoError(GinkgoT(), err)
		defer func() {
			if env := os.Getenv("SKIP_CLEANUP"); env != "" {
				fmt.Printf("Skipping cleanup of role binding %s/%s...\n", roleBinding.GetNamespace(), roleBinding.GetName())
				return
			}
			c.DeleteRoleBinding(roleBinding.GetNamespace(), roleBinding.GetName(), metav1.NewDeleteOptions(0))
		}()
		By(`Create a new NamedInstallStrategy`)
		deploymentName := genName("operator-deployment")
		namedStrategy := newNginxInstallStrategy(deploymentName, permissions, nil)

		aCSV := newCSV(csvName, opGroupNamespace, "", semver.MustParse("0.0.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, nil, &namedStrategy)

		By(`Use the It spec name as label after stripping whitespaces`)
		aCSV.Labels = map[string]string{"label": K8sSafeCurrentTestDescription()}
		createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Create(context.TODO(), &aCSV, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		err = ownerutil.AddOwnerLabels(createdRole, createdCSV)
		require.NoError(GinkgoT(), err)
		createdRole.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue
		_, err = c.UpdateRole(createdRole)
		require.NoError(GinkgoT(), err)

		err = ownerutil.AddOwnerLabels(createdRoleBinding, createdCSV)
		require.NoError(GinkgoT(), err)
		createdRoleBinding.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue
		_, err = c.UpdateRoleBinding(createdRoleBinding)
		require.NoError(GinkgoT(), err)
		GinkgoT().Log("wait for CSV to succeed")
		_, err = fetchCSV(crc, opGroupNamespace, createdCSV.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		GinkgoT().Log("wait for roles to be promoted to clusterroles")
		var fetchedRole *rbacv1.ClusterRole
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedRole, err = c.GetClusterRole(role.GetName())
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		})
		require.EqualValues(GinkgoT(), append(role.Rules, rbacv1.PolicyRule{
			Verbs:     []string{"get", "list", "watch"},
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
		}), fetchedRole.Rules)
		var fetchedRoleBinding *rbacv1.ClusterRoleBinding
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedRoleBinding, err = c.GetClusterRoleBinding(roleBinding.GetName())
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		})
		require.EqualValues(GinkgoT(), roleBinding.Subjects, fetchedRoleBinding.Subjects)
		require.EqualValues(GinkgoT(), roleBinding.RoleRef.Name, fetchedRoleBinding.RoleRef.Name)
		require.EqualValues(GinkgoT(), "rbac.authorization.k8s.io", fetchedRoleBinding.RoleRef.APIGroup)
		require.EqualValues(GinkgoT(), "ClusterRole", fetchedRoleBinding.RoleRef.Kind)
		GinkgoT().Log("ensure operator was granted namespace list permission")
		res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(context.TODO(), &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User: "system:serviceaccount:" + opGroupNamespace + ":" + serviceAccountName,
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Group:    corev1.GroupName,
					Version:  "v1",
					Resource: "namespaces",
					Verb:     "list",
				},
			},
		}, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		require.True(GinkgoT(), res.Status.Allowed, "got %#v", res.Status)
		GinkgoT().Log("Waiting for operator namespace csv to have annotations")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				if apierrors.IsNotFound(fetchErr) {
					return false, nil
				}
				GinkgoT().Logf("Error (in %v): %v", generatedNamespace.GetName(), fetchErr.Error())
				return false, fetchErr
			}
			if checkOperatorGroupAnnotations(fetchedCSV, operatorGroup, true, corev1.NamespaceAll) == nil {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)

		csvList, err := crc.OperatorsV1alpha1().ClusterServiceVersions(corev1.NamespaceAll).List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("label=%s", K8sSafeCurrentTestDescription())})
		require.NoError(GinkgoT(), err)
		GinkgoT().Logf("Found CSV count of %v", len(csvList.Items))
		GinkgoT().Logf("Create other namespace %s", otherNamespaceName)
		otherNamespace := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   otherNamespaceName,
				Labels: matchingLabel,
			},
		}
		_, err = c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &otherNamespace, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err = c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), otherNamespaceName, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()
		GinkgoT().Log("Waiting to ensure copied CSV shows up in other namespace")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				if apierrors.IsNotFound(fetchErr) {
					return false, nil
				}
				GinkgoT().Logf("Error (in %v): %v", otherNamespaceName, fetchErr.Error())
				return false, fetchErr
			}
			if checkOperatorGroupAnnotations(fetchedCSV, operatorGroup, false, "") == nil {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)
		GinkgoT( // verify created CSV is cleaned up after operator group is "contracted"
		).Log("Modifying operator group to no longer watch all namespaces")
		currentOperatorGroup, err := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(context.TODO(), operatorGroup.Name, metav1.GetOptions{})
		require.NoError(GinkgoT(), err)
		currentOperatorGroup.Spec.TargetNamespaces = []string{opGroupNamespace}
		_, err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Update(context.TODO(), currentOperatorGroup, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			GinkgoT().Log("Re-modifying operator group to be watching all namespaces")
			currentOperatorGroup, err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(context.TODO(), operatorGroup.Name, metav1.GetOptions{})
			require.NoError(GinkgoT(), err)
			currentOperatorGroup.Spec = v1.OperatorGroupSpec{}
			_, err = crc.OperatorsV1().OperatorGroups(opGroupNamespace).Update(context.TODO(), currentOperatorGroup, metav1.UpdateOptions{})
			require.NoError(GinkgoT(), err)
		}()

		err = wait.Poll(pollInterval, 2*pollDuration, func() (bool, error) {
			_, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				if apierrors.IsNotFound(fetchErr) {
					return true, nil
				}
				GinkgoT().Logf("Error (in %v): %v", opGroupNamespace, fetchErr.Error())
				return false, fetchErr
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)
	})
	It("insufficient permissions resolve via RBAC", func() {

		log := func(s string) {
			GinkgoT().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
		}

		csvName := genName("another-csv-")

		newNamespaceName := genName(generatedNamespace.GetName() + "-")

		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: newNamespaceName,
			},
		}, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err = c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), newNamespaceName, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		log("Creating CRD")
		mainCRDPlural := genName("opgroup")
		mainCRD := newCRD(mainCRDPlural)
		cleanupCRD, err := createCRD(c, mainCRD)
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		log("Creating operator group")
		serviceAccountName := genName("nginx-sa")
		By(`intentionally creating an operator group without a service account already existing`)
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
		_, err = crc.OperatorsV1().OperatorGroups(newNamespaceName).Create(context.TODO(), &operatorGroup, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		log("Creating CSV")

		By(`Create a new NamedInstallStrategy`)
		deploymentName := genName("operator-deployment")
		namedStrategy := newNginxInstallStrategy(deploymentName, nil, nil)

		aCSV := newCSV(csvName, newNamespaceName, "", semver.MustParse("0.0.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, nil, &namedStrategy)
		createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Create(context.TODO(), &aCSV, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: newNamespaceName,
				Name:      serviceAccountName,
			},
		}
		ownerutil.AddNonBlockingOwner(serviceAccount, createdCSV)
		err = ownerutil.AddOwnerLabels(serviceAccount, createdCSV)
		require.NoError(GinkgoT(), err)

		_, err = c.CreateServiceAccount(serviceAccount)
		require.NoError(GinkgoT(), err)
		By(`Create token secret for the serviceaccount`)
		_, cleanupSE := newTokenSecret(c, newNamespaceName, serviceAccount.GetName())
		defer cleanupSE()

		log("wait for CSV to fail")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Get(context.TODO(), createdCSV.GetName(), metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
			return csvFailedChecker(fetched), nil
		})
		require.NoError(GinkgoT(), err)

		By(`now add cluster admin permissions to service account`)
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
		require.NoError(GinkgoT(), err)

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
		require.NoError(GinkgoT(), err)

		_, err = c.CreateRole(role)
		require.NoError(GinkgoT(), err)
		_, err = c.CreateRoleBinding(roleBinding)
		require.NoError(GinkgoT(), err)

		log("wait for CSV to succeeed")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Get(context.TODO(), createdCSV.GetName(), metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
			return csvSucceededChecker(fetched), nil
		})
		require.NoError(GinkgoT(), err)
	})
	It("insufficient permissions resolve via service account removal", func() {

		log := func(s string) {
			GinkgoT().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
		}

		csvName := genName("another-csv-")

		newNamespaceName := genName(generatedNamespace.GetName() + "-")

		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: newNamespaceName,
			},
		}, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err = c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), newNamespaceName, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		log("Creating CRD")
		mainCRDPlural := genName("opgroup")
		mainCRD := newCRD(mainCRDPlural)
		cleanupCRD, err := createCRD(c, mainCRD)
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		log("Creating operator group")
		serviceAccountName := genName("nginx-sa")
		By(`intentionally creating an operator group without a service account already existing`)
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
		_, err = crc.OperatorsV1().OperatorGroups(newNamespaceName).Create(context.TODO(), &operatorGroup, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		log("Creating CSV")

		By(`Create a new NamedInstallStrategy`)
		deploymentName := genName("operator-deployment")
		namedStrategy := newNginxInstallStrategy(deploymentName, nil, nil)

		aCSV := newCSV(csvName, newNamespaceName, "", semver.MustParse("0.0.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, nil, &namedStrategy)
		createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Create(context.TODO(), &aCSV, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: newNamespaceName,
				Name:      serviceAccountName,
			},
		}
		ownerutil.AddNonBlockingOwner(serviceAccount, createdCSV)
		err = ownerutil.AddOwnerLabels(serviceAccount, createdCSV)
		require.NoError(GinkgoT(), err)

		_, err = c.CreateServiceAccount(serviceAccount)
		require.NoError(GinkgoT(), err)
		By(`Create token secret for the serviceaccount`)
		_, cleanupSE := newTokenSecret(c, newNamespaceName, serviceAccount.GetName())
		defer cleanupSE()

		log("wait for CSV to fail")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Get(context.TODO(), createdCSV.GetName(), metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
			return csvFailedChecker(fetched), nil
		})
		require.NoError(GinkgoT(), err)

		By(`now remove operator group specified service account`)
		createdOpGroup, err := crc.OperatorsV1().OperatorGroups(newNamespaceName).Get(context.TODO(), operatorGroup.GetName(), metav1.GetOptions{})
		require.NoError(GinkgoT(), err)
		createdOpGroup.Spec.ServiceAccountName = ""
		_, err = crc.OperatorsV1().OperatorGroups(newNamespaceName).Update(context.TODO(), createdOpGroup, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		log("wait for CSV to succeeed")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, err := crc.OperatorsV1alpha1().ClusterServiceVersions(newNamespaceName).Get(context.TODO(), createdCSV.GetName(), metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			log(fmt.Sprintf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message))
			return csvSucceededChecker(fetched), nil
		})
		require.NoError(GinkgoT(), err)
	})

	It("cleanup csvs with bad namespace annotation", func() {
		By(`Versions of OLM at 0.14.1 and older had a bug that would place the wrong namespace annotation on copied CSVs,`)
		By(`preventing them from being GCd. This ensures that any leftover CSVs in that state are properly cleared up.`)

		csvName := genName("another-csv-") // must be lowercase for DNS-1123 validation

		opGroupNamespace := generatedNamespace.GetName()
		matchingLabel := map[string]string{"inGroup": opGroupNamespace}
		otherNamespaceName := genName(opGroupNamespace + "-")
		GinkgoT().Log("Creating CRD")
		mainCRDPlural := genName("opgroup-")
		mainCRD := newCRD(mainCRDPlural)
		cleanupCRD, err := createCRD(c, mainCRD)
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()
		operatorGroup, err := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(context.TODO(), fmt.Sprintf("%v-operatorgroup", opGroupNamespace), metav1.GetOptions{})
		require.NoError(GinkgoT(), err)

		expectedOperatorGroupStatus := v1.OperatorGroupStatus{
			Namespaces: []string{metav1.NamespaceAll},
		}
		GinkgoT().Log("Waiting on operator group to have correct status")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetched, fetchErr := crc.OperatorsV1().OperatorGroups(opGroupNamespace).Get(context.TODO(), operatorGroup.Name, metav1.GetOptions{})
			if fetchErr != nil {
				return false, fetchErr
			}
			if len(fetched.Status.Namespaces) > 0 {
				require.ElementsMatch(GinkgoT(), expectedOperatorGroupStatus.Namespaces, fetched.Status.Namespaces)
				fmt.Println(fetched.Status.Namespaces)
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)
		GinkgoT().Log("Creating CSV")
		By(`Generate permissions`)
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
		require.NoError(GinkgoT(), err)
		defer func() {
			if env := os.Getenv("SKIP_CLEANUP"); env != "" {
				fmt.Printf("Skipping cleanup of serviceaccount %s/%s...\n", serviceAccount.GetNamespace(), serviceAccount.GetName())
				return
			}
			c.DeleteServiceAccount(serviceAccount.GetNamespace(), serviceAccount.GetName(), metav1.NewDeleteOptions(0))
		}()
		createdRole, err := c.CreateRole(role)
		require.NoError(GinkgoT(), err)
		defer func() {
			if env := os.Getenv("SKIP_CLEANUP"); env != "" {
				fmt.Printf("Skipping cleanup of role %s/%s...\n", role.GetNamespace(), role.GetName())
				return
			}
			c.DeleteRole(role.GetNamespace(), role.GetName(), metav1.NewDeleteOptions(0))
		}()
		createdRoleBinding, err := c.CreateRoleBinding(roleBinding)
		require.NoError(GinkgoT(), err)
		defer func() {
			if env := os.Getenv("SKIP_CLEANUP"); env != "" {
				fmt.Printf("Skipping cleanup of role binding %s/%s...\n", roleBinding.GetNamespace(), roleBinding.GetName())
				return
			}
			c.DeleteRoleBinding(roleBinding.GetNamespace(), roleBinding.GetName(), metav1.NewDeleteOptions(0))
		}()
		By(`Create a new NamedInstallStrategy`)
		deploymentName := genName("operator-deployment")
		namedStrategy := newNginxInstallStrategy(deploymentName, permissions, nil)

		aCSV := newCSV(csvName, opGroupNamespace, "", semver.MustParse("0.0.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, nil, &namedStrategy)

		By(`Use the It spec name as label after stripping whitespaces`)
		aCSV.Labels = map[string]string{"label": K8sSafeCurrentTestDescription()}
		createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Create(context.TODO(), &aCSV, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		err = ownerutil.AddOwnerLabels(createdRole, createdCSV)
		require.NoError(GinkgoT(), err)
		createdRole.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue
		_, err = c.UpdateRole(createdRole)
		require.NoError(GinkgoT(), err)

		err = ownerutil.AddOwnerLabels(createdRoleBinding, createdCSV)
		require.NoError(GinkgoT(), err)
		createdRoleBinding.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue
		_, err = c.UpdateRoleBinding(createdRoleBinding)
		require.NoError(GinkgoT(), err)
		GinkgoT().Log("wait for CSV to succeed")
		_, err = fetchCSV(crc, opGroupNamespace, createdCSV.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		GinkgoT().Log("wait for roles to be promoted to clusterroles")
		var fetchedRole *rbacv1.ClusterRole
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedRole, err = c.GetClusterRole(role.GetName())
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		})
		require.NoError(GinkgoT(), err)
		require.EqualValues(GinkgoT(), append(role.Rules, rbacv1.PolicyRule{
			Verbs:     []string{"get", "list", "watch"},
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
		}), fetchedRole.Rules)
		var fetchedRoleBinding *rbacv1.ClusterRoleBinding
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedRoleBinding, err = c.GetClusterRoleBinding(roleBinding.GetName())
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		})
		require.NoError(GinkgoT(), err)
		require.EqualValues(GinkgoT(), roleBinding.Subjects, fetchedRoleBinding.Subjects)
		require.EqualValues(GinkgoT(), roleBinding.RoleRef.Name, fetchedRoleBinding.RoleRef.Name)
		require.EqualValues(GinkgoT(), "rbac.authorization.k8s.io", fetchedRoleBinding.RoleRef.APIGroup)
		require.EqualValues(GinkgoT(), "ClusterRole", fetchedRoleBinding.RoleRef.Kind)
		GinkgoT().Log("ensure operator was granted namespace list permission")
		res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(context.TODO(), &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User: "system:serviceaccount:" + opGroupNamespace + ":" + serviceAccountName,
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Group:    corev1.GroupName,
					Version:  "v1",
					Resource: "namespaces",
					Verb:     "list",
				},
			},
		}, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		require.True(GinkgoT(), res.Status.Allowed, "got %#v", res.Status)
		GinkgoT().Log("Waiting for operator namespace csv to have annotations")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(opGroupNamespace).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				if apierrors.IsNotFound(fetchErr) {
					return false, nil
				}
				GinkgoT().Logf("Error (in %v): %v", generatedNamespace.GetName(), fetchErr.Error())
				return false, fetchErr
			}
			if checkOperatorGroupAnnotations(fetchedCSV, operatorGroup, true, corev1.NamespaceAll) == nil {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)

		csvList, err := crc.OperatorsV1alpha1().ClusterServiceVersions(corev1.NamespaceAll).List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("label=%s", K8sSafeCurrentTestDescription())})
		require.NoError(GinkgoT(), err)
		GinkgoT().Logf("Found CSV count of %v", len(csvList.Items))
		GinkgoT().Logf("Create other namespace %s", otherNamespaceName)
		otherNamespace := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   otherNamespaceName,
				Labels: matchingLabel,
			},
		}
		_, err = c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &otherNamespace, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err = c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), otherNamespaceName, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()
		GinkgoT().Log("Waiting to ensure copied CSV shows up in other namespace")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				if apierrors.IsNotFound(fetchErr) {
					return false, nil
				}
				GinkgoT().Logf("Error (in %v): %v", otherNamespaceName, fetchErr.Error())
				return false, fetchErr
			}
			if checkOperatorGroupAnnotations(fetchedCSV, operatorGroup, false, "") == nil {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)
		GinkgoT().Log("Copied CSV showed up in other namespace, giving copied CSV a bad OpertorGroup annotation")
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedCSV, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				return false, fetchErr
			}
			fetchedCSV.Annotations[v1.OperatorGroupNamespaceAnnotationKey] = fetchedCSV.GetNamespace()
			_, updateErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Update(context.TODO(), fetchedCSV, metav1.UpdateOptions{})
			if updateErr != nil {
				GinkgoT().Logf("Error updating copied CSV (in %v): %v", otherNamespaceName, updateErr.Error())
				return false, updateErr
			}
			return true, nil
		})
		require.NoError(GinkgoT(), err)
		GinkgoT().Log("Done updating copied CSV with bad annotation OperatorGroup, waiting for CSV to be gc'd")
		err = wait.Poll(pollInterval, 2*pollDuration, func() (bool, error) {
			csv, fetchErr := crc.OperatorsV1alpha1().ClusterServiceVersions(otherNamespaceName).Get(context.TODO(), csvName, metav1.GetOptions{})
			if fetchErr != nil {
				if apierrors.IsNotFound(fetchErr) {
					return true, nil
				}
				GinkgoT().Logf("Error (in %v): %v", opGroupNamespace, fetchErr.Error())
				return false, fetchErr
			}
			By(`The CSV with the wrong annotation could have been replaced with a new copied CSV by this time`)
			By(`If we find a CSV in the namespace, and it contains the correct annotation, it means the CSV`)
			By(`with the wrong annotation was GCed`)
			if csv.Annotations[v1.OperatorGroupNamespaceAnnotationKey] != csv.GetNamespace() {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err)
	})
	It("OperatorGroupLabels", func() {

		By(`Create the namespaces that will have an OperatorGroup Label applied.`)
		testNamespaceA := genName("namespace-a-")
		testNamespaceB := genName("namespace-b-")
		testNamespaceC := genName("namespace-c-")
		testNamespaces := []string{
			testNamespaceA, testNamespaceB, testNamespaceC,
		}

		By(`Create the namespaces`)
		for _, namespace := range testNamespaces {
			_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}, metav1.CreateOptions{})
			require.NoError(GinkgoT(), err)
		}

		By(`Cleanup namespaces`)
		defer func() {
			for _, namespace := range testNamespaces {
				err := c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})
				require.NoError(GinkgoT(), err)
			}
		}()

		By(`Create an OperatorGroup`)
		operatorGroup := &v1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("e2e-operator-group-"),
				Namespace: testNamespaceA,
			},
			Spec: v1.OperatorGroupSpec{
				TargetNamespaces: []string{},
			},
		}
		operatorGroup, err := crc.OperatorsV1().OperatorGroups(testNamespaceA).Create(context.TODO(), operatorGroup, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Cleanup OperatorGroup`)
		defer func() {
			err := crc.OperatorsV1().OperatorGroups(testNamespaceA).Delete(context.TODO(), operatorGroup.GetName(), metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		By(`Create the OperatorGroup Label`)
		ogLabel, err := getOGLabelKey(operatorGroup)
		require.NoError(GinkgoT(), err)

		By(`Create list options`)
		listOptions := metav1.ListOptions{
			LabelSelector: labels.Set(map[string]string{ogLabel: ""}).String(),
		}

		namespaceList, err := pollForNamespaceListCount(c, listOptions, 0)
		require.NoError(GinkgoT(), err)

		By(`Update the OperatorGroup to include a single namespace`)
		operatorGroup.Spec.TargetNamespaces = []string{testNamespaceA}
		updateOGSpecFunc := updateOperatorGroupSpecFunc(GinkgoT(), crc, testNamespaceA, operatorGroup.GetName())
		require.NoError(GinkgoT(), retry.RetryOnConflict(retry.DefaultBackoff, updateOGSpecFunc(operatorGroup.Spec)))

		namespaceList, err = pollForNamespaceListCount(c, listOptions, 1)
		require.NoError(GinkgoT(), err)
		require.True(GinkgoT(), checkForOperatorGroupLabels(operatorGroup, namespaceList.Items))

		By(`Update the OperatorGroup to include two namespaces`)
		operatorGroup.Spec.TargetNamespaces = []string{testNamespaceA, testNamespaceC}
		require.NoError(GinkgoT(), retry.RetryOnConflict(retry.DefaultBackoff, updateOGSpecFunc(operatorGroup.Spec)))

		namespaceList, err = pollForNamespaceListCount(c, listOptions, 2)
		require.NoError(GinkgoT(), err)
		require.True(GinkgoT(), checkForOperatorGroupLabels(operatorGroup, namespaceList.Items))

		By(`Update the OperatorGroup to include three namespaces`)
		operatorGroup.Spec.TargetNamespaces = []string{testNamespaceA, testNamespaceB, testNamespaceC}
		require.NoError(GinkgoT(), retry.RetryOnConflict(retry.DefaultBackoff, updateOGSpecFunc(operatorGroup.Spec)))

		namespaceList, err = pollForNamespaceListCount(c, listOptions, 3)
		require.NoError(GinkgoT(), err)
		require.True(GinkgoT(), checkForOperatorGroupLabels(operatorGroup, namespaceList.Items))

		By(`Update the OperatorGroup to include two namespaces`)
		operatorGroup.Spec.TargetNamespaces = []string{testNamespaceA, testNamespaceC}
		require.NoError(GinkgoT(), retry.RetryOnConflict(retry.DefaultBackoff, updateOGSpecFunc(operatorGroup.Spec)))

		namespaceList, err = pollForNamespaceListCount(c, listOptions, 2)
		require.NoError(GinkgoT(), err)
		require.True(GinkgoT(), checkForOperatorGroupLabels(operatorGroup, namespaceList.Items))

		By(`Make the OperatorGroup a Cluster OperatorGroup.`)
		operatorGroup.Spec.TargetNamespaces = []string{}
		require.NoError(GinkgoT(), retry.RetryOnConflict(retry.DefaultBackoff, updateOGSpecFunc(operatorGroup.Spec)))

		namespaceList, err = pollForNamespaceListCount(c, listOptions, 0)
		require.NoError(GinkgoT(), err)
	})
	It("CleanupDeletedOperatorGroupLabels", func() {

		By(`Create the namespaces that will have an OperatorGroup Label applied.`)
		testNamespaceA := genName("namespace-a-")
		testNamespaceB := genName("namespace-b-")
		testNamespaceC := genName("namespace-c-")
		testNamespaces := []string{
			testNamespaceA, testNamespaceB, testNamespaceC,
		}

		By(`Create the namespaces`)
		for _, namespace := range testNamespaces {
			_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}, metav1.CreateOptions{})
			require.NoError(GinkgoT(), err)
		}

		By(`Cleanup namespaces`)
		defer func() {
			for _, namespace := range testNamespaces {
				err := c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})
				require.NoError(GinkgoT(), err)
			}
		}()

		By(`Create an OperatorGroup with three target namespaces.`)
		operatorGroup := &v1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("e2e-operator-group-"),
				Namespace: testNamespaceA,
			},
			Spec: v1.OperatorGroupSpec{
				TargetNamespaces: testNamespaces,
			},
		}
		operatorGroup, err := crc.OperatorsV1().OperatorGroups(testNamespaceA).Create(context.TODO(), operatorGroup, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Create the OperatorGroup Label`)
		ogLabel, err := getOGLabelKey(operatorGroup)
		require.NoError(GinkgoT(), err)

		By(`Create list options`)
		listOptions := metav1.ListOptions{
			LabelSelector: labels.Set(map[string]string{ogLabel: ""}).String(),
		}

		namespaceList, err := pollForNamespaceListCount(c, listOptions, 3)
		require.NoError(GinkgoT(), err)
		require.True(GinkgoT(), checkForOperatorGroupLabels(operatorGroup, namespaceList.Items))

		By(`Delete the operatorGroup.`)
		err = crc.OperatorsV1().OperatorGroups(testNamespaceA).Delete(context.TODO(), operatorGroup.GetName(), metav1.DeleteOptions{})
		require.NoError(GinkgoT(), err)

		By(`Check that no namespaces have the OperatorGroup.`)
		namespaceList, err = pollForNamespaceListCount(c, listOptions, 0)
		require.NoError(GinkgoT(), err)
	})

	Context("Given a set of Namespaces", func() {
		var (
			c              operatorclient.ClientInterface
			crc            versioned.Interface
			testNamespaces []string
			testNamespaceA string
		)

		BeforeEach(func() {
			c = newKubeClient()
			crc = newCRClient()

			By(`Create the namespaces that will have an OperatorGroup Label applied.`)
			testNamespaceA = genName("namespace-a-")
			testNamespaceB := genName("namespace-b-")
			testNamespaceC := genName("namespace-c-")
			testNamespaces = []string{
				testNamespaceA, testNamespaceB, testNamespaceC,
			}

			By(`Create the namespaces`)
			for _, namespace := range testNamespaces {
				_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: namespace,
					},
				}, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())
			}
		})

		AfterEach(func() {
			By(`Cleanup namespaces`)
			for _, namespace := range testNamespaces {
				err := c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
			}
		})

		Context("Associating these Namespaces with a label", func() {
			var (
				matchingLabel map[string]string
			)

			BeforeEach(func() {
				matchingLabel = map[string]string{"foo": "bar"}

				By(`Updating Namespace with labels`)
				for _, namespace := range testNamespaces {
					_, err := c.KubernetesInterface().CoreV1().Namespaces().Update(context.TODO(), &corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:   namespace,
							Labels: matchingLabel,
						},
					}, metav1.UpdateOptions{})
					Expect(err).ToNot(HaveOccurred())
				}

			})

			When("an OperatorGroup is created having matching label selector defined", func() {
				var operatorGroup *v1.OperatorGroup

				BeforeEach(func() {
					By(`Creating operator group`)
					operatorGroup = &v1.OperatorGroup{
						ObjectMeta: metav1.ObjectMeta{
							Name:      genName("e2e-operator-group-"),
							Namespace: testNamespaceA,
						},
						Spec: v1.OperatorGroupSpec{
							Selector: &metav1.LabelSelector{
								MatchLabels: matchingLabel,
							},
						},
					}
					var err error
					operatorGroup, err = crc.OperatorsV1().OperatorGroups(testNamespaceA).Create(context.TODO(), operatorGroup, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())
				})

				It("[FLAKE] OLM applies labels to Namespaces that are associated with an OperatorGroup", func() {
					By(`issue: https://github.com/operator-framework/operator-lifecycle-manager/issues/2637`)
					ogLabel, err := getOGLabelKey(operatorGroup)
					Expect(err).ToNot(HaveOccurred())

					By(`Create list options`)
					listOptions := metav1.ListOptions{
						LabelSelector: labels.Set(map[string]string{ogLabel: ""}).String(),
					}

					By(`Verify that all the namespaces listed in targetNamespaces field of OperatorGroup have labels applied on them`)
					namespaceList, err := pollForNamespaceListCount(c, listOptions, 3)
					Expect(err).ToNot(HaveOccurred())
					Expect(checkForOperatorGroupLabels(operatorGroup, namespaceList.Items)).Should(BeTrue())
				})
			})
		})

		When("an OperatorGroup is created having above Namespaces defined under targetNamespaces field", func() {
			var operatorGroup *v1.OperatorGroup

			BeforeEach(func() {
				By(`Create an OperatorGroup with three target namespaces.`)
				operatorGroup = &v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      genName("e2e-operator-group-"),
						Namespace: testNamespaceA,
					},
					Spec: v1.OperatorGroupSpec{
						TargetNamespaces: testNamespaces,
					},
				}
				var err error
				operatorGroup, err = crc.OperatorsV1().OperatorGroups(testNamespaceA).Create(context.TODO(), operatorGroup, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())
			})

			It("OLM applies labels to Namespaces that are associated with an OperatorGroup", func() {

				ogLabel, err := getOGLabelKey(operatorGroup)
				Expect(err).ToNot(HaveOccurred())

				By(`Create list options`)
				listOptions := metav1.ListOptions{
					LabelSelector: labels.Set(map[string]string{ogLabel: ""}).String(),
				}

				By(`Verify that all the namespaces listed in targetNamespaces field of OperatorGroup have labels applied on them`)
				namespaceList, err := pollForNamespaceListCount(c, listOptions, 3)
				Expect(err).ToNot(HaveOccurred())
				Expect(checkForOperatorGroupLabels(operatorGroup, namespaceList.Items)).Should(BeTrue())

			})
		})
	})
})

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

func createProjectAdmin(t GinkgoTInterface, c operatorclient.ClientInterface, namespace string) (string, cleanupFunc) {
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

	return "system:serviceaccount:" + namespace + ":" + sa.GetName(), func() {
		_ = c.DeleteServiceAccount(sa.GetNamespace(), sa.GetName(), metav1.NewDeleteOptions(0))
		_ = c.DeleteRoleBinding(rb.GetNamespace(), rb.GetName(), metav1.NewDeleteOptions(0))
	}
}

func checkForOperatorGroupLabels(operatorGroup *v1.OperatorGroup, namespaces []corev1.Namespace) bool {
	for _, ns := range operatorGroup.Spec.TargetNamespaces {
		if !containsNamespace(namespaces, ns) {
			return false
		}
	}
	return true
}

func updateOperatorGroupSpecFunc(t GinkgoTInterface, crc versioned.Interface, namespace, operatorGroupName string) func(v1.OperatorGroupSpec) func() error {
	return func(operatorGroupSpec v1.OperatorGroupSpec) func() error {
		return func() error {
			fetchedOG, err := crc.OperatorsV1().OperatorGroups(namespace).Get(context.TODO(), operatorGroupName, metav1.GetOptions{})
			require.NoError(t, err)
			fetchedOG.Spec = operatorGroupSpec
			_, err = crc.OperatorsV1().OperatorGroups(namespace).Update(context.TODO(), fetchedOG, metav1.UpdateOptions{})
			return err
		}
	}
}

func pollForNamespaceListCount(c operatorclient.ClientInterface, listOptions metav1.ListOptions, expectedLength int) (list *corev1.NamespaceList, err error) {
	Eventually(func() (bool, error) {
		list, err = c.KubernetesInterface().CoreV1().Namespaces().List(context.TODO(), listOptions)
		if err != nil {
			return false, err
		}
		if len(list.Items) == expectedLength {
			return true, nil
		}
		return false, fmt.Errorf("expected %d resources, got %d", expectedLength, len(list.Items))
	}).Should(BeTrue())
	return
}

func containsNamespace(namespaces []corev1.Namespace, namespaceName string) bool {
	for i := range namespaces {
		if namespaces[i].GetName() == namespaceName {
			return true
		}
	}
	return false
}

func getOGLabelKey(og *v1.OperatorGroup) (string, error) {
	ogUID := string(og.GetUID())
	if ogUID == "" {
		return "", fmt.Errorf("OperatorGroup UID is empty string")
	}
	return fmt.Sprintf("olm.operatorgroup.uid/%s", og.GetUID()), nil
}
