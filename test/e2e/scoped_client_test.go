package e2e

import (
	"context"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/scoped"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Scoped Client bound to a service account can be used to make API calls", func() {
	// TestScopedClient ensures that we can create a scoped client bound to a
	// service account and then we can use the scoped client to make API calls.
	var (
		config *rest.Config

		kubeclient    operatorclient.ClientInterface
		crclient      versioned.Interface
		dynamicclient dynamic.Interface

		logger *logrus.Logger
	)

	BeforeEach(func() {
		config = ctx.Ctx().RESTConfig()

		kubeclient = newKubeClient()
		crclient = newCRClient()
		dynamicclient = ctx.Ctx().DynamicClient()

		logger = logrus.New()
		logger.SetOutput(GinkgoWriter)
	})

	type testParameter struct {
		name       string
		grant      func(namespace, name string) (cleanup cleanupFunc)
		assertFunc func(errGot error)
	}

	tableEntries := []table.TableEntry{
		// The parent test invokes 'Get' API on non existent objects. If the
		// scoped client has enough permission, we expect a NotFound error code.
		// Otherwise, we expect a 'Forbidden' error code due to lack of permission.

		table.Entry("returns error on API calls as ServiceAccount does not have any permission", testParameter{
			// The service account does not have any permission granted to it.
			// We expect the get api call to return 'Forbidden' error due to
			// lack of permission.
			assertFunc: func(errGot error) {
				Expect(k8serrors.IsForbidden(errGot)).To(BeTrue())
			},
		}),
		table.Entry("successfully allows API calls to be made when ServiceAccount has permission", testParameter{
			// The service account does have permission granted to it.
			// We expect the get api call to return 'NotFound' error.
			grant: func(namespace, name string) (cleanup cleanupFunc) {
				cleanup = grantPermission(GinkgoT(), kubeclient, namespace, name)
				return
			},
			assertFunc: func(errGot error) {
				Expect(k8serrors.IsNotFound(errGot)).To(BeTrue())
			},
		}),
	}

	table.DescribeTable("API call using scoped client", func(tc testParameter) {
		// Steps:
		// 1. Create a new namespace
		// 2. Create a service account.
		// 3. Grant permission(s) to the service account if specified.
		// 4. Get scoped client instance(s)
		// 5. Invoke Get API call on non existent object(s) to check if
		//    the call can be made successfully.
		namespace := genName("a")
		_, cleanupNS := newNamespace(kubeclient, namespace)
		defer cleanupNS()

		saName := genName("user-defined-")
		sa, cleanupSA := newServiceAccount(kubeclient, namespace, saName)
		defer cleanupSA()

		By("Wait for ServiceAccount secret to be available")
		Eventually(func() (*corev1.ServiceAccount, error) {
			sa, err := kubeclient.KubernetesInterface().CoreV1().ServiceAccounts(sa.GetNamespace()).Get(context.TODO(), sa.GetName(), metav1.GetOptions{})
			return sa, err
		}).ShouldNot(WithTransform(func(v *corev1.ServiceAccount) []corev1.ObjectReference {
			return v.Secrets
		}, BeEmpty()))

		strategy := scoped.NewClientAttenuator(logger, config, kubeclient, crclient, dynamicclient)
		getter := func() (reference *corev1.ObjectReference, err error) {
			reference = &corev1.ObjectReference{
				Namespace: namespace,
				Name:      saName,
			}
			return
		}

		if tc.grant != nil {
			cleanupPerm := tc.grant(sa.GetNamespace(), sa.GetName())
			defer cleanupPerm()
		}

		By("Get scoped client instance(s)")
		kubeclientGot, crclientGot, dynamicClientGot, errGot := strategy.AttenuateClient(getter)
		Expect(errGot).ToNot(HaveOccurred())
		Expect(kubeclientGot).ToNot(BeNil())
		Expect(crclientGot).ToNot(BeNil())
		Expect(dynamicClientGot).ToNot(BeNil())

		_, errGot = kubeclientGot.KubernetesInterface().CoreV1().ConfigMaps(namespace).Get(context.TODO(), genName("does-not-exist-"), metav1.GetOptions{})
		Expect(errGot).To(HaveOccurred())
		tc.assertFunc(errGot)

		_, errGot = crclientGot.OperatorsV1alpha1().CatalogSources(namespace).Get(context.TODO(), genName("does-not-exist-"), metav1.GetOptions{})
		Expect(errGot).To(HaveOccurred())
		tc.assertFunc(errGot)

		gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "ConfigMap"}
		_, errGot = dynamicClientGot.Resource(gvr).Namespace(namespace).Get(context.TODO(), genName("does-not-exist-"), metav1.GetOptions{})
		Expect(errGot).To(HaveOccurred())
		tc.assertFunc(errGot)
	}, tableEntries...)
})
