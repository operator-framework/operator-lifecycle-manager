package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clients"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/scoped"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Scoped Client bound to a service account can be used to make API calls", func() {
	// TestScopedClient ensures that we can create a scoped client bound to a
	// service account and then we can use the scoped client to make API calls.
	var (
		config *rest.Config

		kubeclient operatorclient.ClientInterface

		logger *logrus.Logger
	)

	BeforeEach(func() {
		config = ctx.Ctx().RESTConfig()

		kubeclient = ctx.Ctx().KubeClient()

		logger = logrus.New()
		logger.SetOutput(GinkgoWriter)
	})

	type testParameter struct {
		name       string
		grant      func(namespace, name string) (cleanup cleanupFunc)
		assertFunc func(errGot error)
	}

	tableEntries := []TableEntry{
		// The parent test invokes 'Get' API on non existent objects. If the
		// scoped client has enough permission, we expect a NotFound error code.
		// Otherwise, we expect a 'Forbidden' error code due to lack of permission.

		Entry("returns error on API calls as ServiceAccount does not have any permission", testParameter{
			// The service account does not have any permission granted to it.
			// We expect the get api call to return 'Forbidden' error due to
			// lack of permission.
			assertFunc: func(errGot error) {
				Expect(apierrors.IsForbidden(errGot)).To(BeTrue())
			},
		}),
		Entry("successfully allows API calls to be made when ServiceAccount has permission", testParameter{
			// The service account does have permission granted to it.
			// We expect the get api call to return 'NotFound' error.
			grant: func(namespace, name string) (cleanup cleanupFunc) {
				cleanup = grantPermission(GinkgoT(), kubeclient, namespace, name)
				return
			},
			assertFunc: func(errGot error) {
				Expect(apierrors.IsNotFound(errGot)).To(BeTrue())
			},
		}),
	}

	DescribeTable("API call using scoped client", func(tc testParameter) {
		By(`Create a new namespace`)
		namespace := genName("a")
		_, cleanupNS := newNamespace(kubeclient, namespace)
		defer cleanupNS()

		By(`Create a service account.`)
		saName := genName("user-defined-")
		sa, cleanupSA := newServiceAccount(kubeclient, namespace, saName)
		defer cleanupSA()
		By(`Create token secret for the serviceaccount`)
		secret, cleanupSE := newTokenSecret(kubeclient, namespace, saName)
		defer cleanupSE()

		By("Wait for token secret data to be available")
		Eventually(func() (*corev1.Secret, error) {
			se, err := kubeclient.KubernetesInterface().CoreV1().Secrets(secret.GetNamespace()).Get(context.TODO(), secret.GetName(), metav1.GetOptions{})
			return se, err
		}).ShouldNot(WithTransform(func(v *corev1.Secret) string {
			return string(v.Data[corev1.ServiceAccountTokenKey])
		}, BeEmpty()))

		By(`Grant permission(s) to the service account if specified.`)
		strategy := scoped.NewClientAttenuator(logger, config, kubeclient)
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
		attenuate, err := strategy.AttenuateToServiceAccount(getter)
		Expect(err).ToNot(HaveOccurred())

		factory := clients.NewFactory(config).WithConfigTransformer(attenuate)
		kubeclientGot, err := factory.NewOperatorClient()
		Expect(err).ToNot(HaveOccurred())
		Expect(kubeclientGot).ToNot(BeNil())
		crclientGot, err := factory.NewKubernetesClient()
		Expect(err).ToNot(HaveOccurred())
		Expect(crclientGot).ToNot(BeNil())
		dynamicClientGot, err := factory.NewDynamicClient()
		Expect(err).ToNot(HaveOccurred())
		Expect(dynamicClientGot).ToNot(BeNil())

		By(`Invoke Get API call on non existent object(s) to check if the call can be made successfully.`)
		_, err = kubeclientGot.KubernetesInterface().CoreV1().ConfigMaps(namespace).Get(context.TODO(), genName("does-not-exist-"), metav1.GetOptions{})
		Expect(err).To(HaveOccurred())
		tc.assertFunc(err)

		_, err = crclientGot.OperatorsV1alpha1().CatalogSources(namespace).Get(context.TODO(), genName("does-not-exist-"), metav1.GetOptions{})
		Expect(err).To(HaveOccurred())
		tc.assertFunc(err)

		gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "ConfigMap"}
		_, err = dynamicClientGot.Resource(gvr).Namespace(namespace).Get(context.TODO(), genName("does-not-exist-"), metav1.GetOptions{})
		Expect(err).To(HaveOccurred())
		tc.assertFunc(err)
	}, tableEntries)
})
