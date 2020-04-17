package e2e

import (
	"fmt"

	"github.com/blang/semver"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	. "github.com/operator-framework/operator-lifecycle-manager/test/e2e/dsl"
)

var _ = Describe("Garbage collector", func() {
	var (
		kubeClient     operatorclient.ClientInterface
		operatorClient versioned.Interface
	)

	BeforeEach(func() {
		kubeClient = ctx.Ctx().KubeClient()
		operatorClient = ctx.Ctx().OperatorClient()
	})

	It("should delete a ClusterRole owned by a CustomResourceDefinition when the owner is deleted", func() {
		group := fmt.Sprintf("%s.com", rand.String(16))
		crd, err := kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Create(&apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("plural.%s", group),
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: group,
				Scope: apiextensionsv1.ClusterScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					apiextensionsv1.CustomResourceDefinitionVersion{
						Name:    "v1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
						},
					},
				},
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "plural",
					Singular: "singular",
					Kind:     "Kind",
					ListKind: "KindList",
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			IgnoreError(kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(crd.GetName(), &metav1.DeleteOptions{}))
		}()

		cr, err := kubeClient.CreateClusterRole(&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName:    "clusterrole-",
				OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(crd)},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			IgnoreError(kubeClient.DeleteClusterRole(cr.GetName(), &metav1.DeleteOptions{}))
		}()

		Expect(kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(crd.GetName(), &metav1.DeleteOptions{})).To(Succeed())
		Eventually(func() bool {
			_, err := kubeClient.GetClusterRole(cr.GetName())
			return k8serrors.IsNotFound(err)
		}).Should(BeTrue(), "get cluster role should eventually return \"not found\"")
	})

	It("should delete a ClusterRole owned by an APIService when the owner is deleted", func() {
		group := rand.String(16)
		as, err := kubeClient.CreateAPIService(&apiregistrationv1.APIService{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("v1.%s", group),
			},
			Spec: apiregistrationv1.APIServiceSpec{
				Group:                group,
				Version:              "v1",
				GroupPriorityMinimum: 1,
				VersionPriority:      1,
			},
		})
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			IgnoreError(kubeClient.DeleteAPIService(as.GetName(), &metav1.DeleteOptions{}))
		}()

		cr, err := kubeClient.CreateClusterRole(&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName:    "clusterrole-",
				OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(as)},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			IgnoreError(kubeClient.DeleteClusterRole(cr.GetName(), &metav1.DeleteOptions{}))
		}()

		Expect(kubeClient.DeleteAPIService(as.GetName(), &metav1.DeleteOptions{})).To(Succeed())
		Eventually(func() bool {
			_, err := kubeClient.GetClusterRole(cr.GetName())
			return k8serrors.IsNotFound(err)
		}).Should(BeTrue(), "get cluster role should eventually return \"not found\"")
	})

	It("owner reference GC behavior", func() {

		// TestOwnerReferenceGCBehavior runs a simple check on OwnerReference behavior to ensure
		// a resource with multiple OwnerReferences will not be garbage collected when one of its
		// owners has been deleted.
		// Test Case:
		//				CSV-A     CSV-B                        CSV-B
		//				   \      /      --Delete CSV-A-->       |
		//				   ConfigMap						 ConfigMap

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		ownerA := newCSV("ownera", testNamespace, "", semver.MustParse("0.0.0"), nil, nil, newNginxInstallStrategy("dep-", nil, nil))
		ownerB := newCSV("ownerb", testNamespace, "", semver.MustParse("0.0.0"), nil, nil, newNginxInstallStrategy("dep-", nil, nil))

		// create all owners
		fetchedA, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(&ownerA)
		require.NoError(GinkgoT(), err)
		fetchedB, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(&ownerB)
		require.NoError(GinkgoT(), err)

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
		_, err = kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Create(dependent)
		require.NoError(GinkgoT(), err, "dependent could not be created")

		// delete ownerA in the foreground (to ensure any "blocking" dependents are deleted before ownerA)
		propagation := metav1.DeletionPropagation("Foreground")
		options := metav1.DeleteOptions{PropagationPolicy: &propagation}
		err = operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(fetchedA.GetName(), &options)
		require.NoError(GinkgoT(), err)

		// wait for deletion of ownerA
		waitForDelete(func() error {
			_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(ownerA.GetName(), metav1.GetOptions{})
			return err
		})

		// check for dependent (should still exist since it still has one owner present)
		_, err = kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(dependent.GetName(), metav1.GetOptions{})
		require.NoError(GinkgoT(), err, "dependent deleted after one owner was deleted")
		GinkgoT().Log("dependent still exists after one owner was deleted")

		// delete ownerB in the foreground (to ensure any "blocking" dependents are deleted before ownerB)
		err = operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(fetchedB.GetName(), &options)
		require.NoError(GinkgoT(), err)

		// wait for deletion of ownerB
		waitForDelete(func() error {
			_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(ownerB.GetName(), metav1.GetOptions{})
			return err
		})

		// check for dependent (should be deleted since last blocking owner was deleted)
		_, err = kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(dependent.GetName(), metav1.GetOptions{})
		require.Error(GinkgoT(), err)
		require.True(GinkgoT(), k8serrors.IsNotFound(err))
		GinkgoT().Log("dependent successfully garbage collected after both owners were deleted")
	})
})
