package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/scoped"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Scoped Client", func() {
	// TestScopedClient ensures that we can create a scoped client bound to a
	// service account and then we can use the scoped client to make API calls.

	var config *rest.Config

	var kubeclient operatorclient.ClientInterface
	var crclient versioned.Interface
	var dynamicclient dynamic.Interface

	var logger *logrus.Logger

	BeforeEach(func() {
		config = ctx.Ctx().RESTConfig()

		kubeclient = newKubeClient(GinkgoT())
		crclient = newCRClient(GinkgoT())
		dynamicclient = ctx.Ctx().DynamicClient()

		logger = logrus.New()
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

		table.Entry("ServiceAccountDoesNotHaveAnyPermission", testParameter{
			// The service account does not have any permission granted to it.
			// We expect the get api call to return 'Forbidden' error due to
			// lack of permission.
			name: "ServiceAccountDoesNotHaveAnyPermission",
			assertFunc: func(errGot error) {
				require.True(GinkgoT(), k8serrors.IsForbidden(errGot))
			},
		}),
		table.Entry("ServiceAccountHasPermission", testParameter{
			// The service account does have permission granted to it.
			// We expect the get api call to return 'NotFound' error.
			name: "ServiceAccountHasPermission",
			grant: func(namespace, name string) (cleanup cleanupFunc) {
				cleanup = grantPermission(GinkgoT(), kubeclient, namespace, name)
				return
			},
			assertFunc: func(errGot error) {
				require.True(GinkgoT(), k8serrors.IsNotFound(errGot))
			},
		}),
	}

	table.DescribeTable("Test", func(tt testParameter) {
		// Steps:
		// 1. Create a new namespace
		// 2. Create a service account.
		// 3. Grant permission(s) to the service account if specified.
		// 4. Get scoped client instance(s)
		// 5. Invoke Get API call on non existent object(s) to check if
		//    the call can be made successfully.
		namespace := genName("a")
		_, cleanupNS := newNamespace(GinkgoT(), kubeclient, namespace)
		defer cleanupNS()

		saName := genName("user-defined-")
		sa, cleanupSA := newServiceAccount(GinkgoT(), kubeclient, namespace, saName)
		defer cleanupSA()

		waitForServiceAccountSecretAvailable(GinkgoT(), kubeclient, sa.GetNamespace(), sa.GetName())

		strategy := scoped.NewClientAttenuator(logger, config, kubeclient, crclient, dynamicclient)
		getter := func() (reference *corev1.ObjectReference, err error) {
			reference = &corev1.ObjectReference{
				Namespace: namespace,
				Name:      saName,
			}

			return
		}

		if tt.grant != nil {
			cleanupPerm := tt.grant(sa.GetNamespace(), sa.GetName())
			defer cleanupPerm()
		}

		// We expect to get scoped client instance(s).
		kubeclientGot, crclientGot, dynamicClientGot, errGot := strategy.AttenuateClient(getter)
		require.NoError(GinkgoT(), errGot)
		require.NotNil(GinkgoT(), kubeclientGot)
		require.NotNil(GinkgoT(), crclientGot)

		_, errGot = kubeclientGot.KubernetesInterface().CoreV1().ConfigMaps(namespace).Get(context.TODO(), genName("does-not-exist-"), metav1.GetOptions{})
		require.Error(GinkgoT(), errGot)
		tt.assertFunc(errGot)

		_, errGot = crclientGot.OperatorsV1alpha1().CatalogSources(namespace).Get(context.TODO(), genName("does-not-exist-"), metav1.GetOptions{})
		require.Error(GinkgoT(), errGot)
		tt.assertFunc(errGot)

		gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "ConfigMap"}
		_, errGot = dynamicClientGot.Resource(gvr).Namespace(namespace).Get(context.TODO(), genName("does-not-exist-"), metav1.GetOptions{})
		require.Error(GinkgoT(), errGot)
		tt.assertFunc(errGot)
	}, tableEntries...)
})

func waitForServiceAccountSecretAvailable(t GinkgoTInterface, client operatorclient.ClientInterface, namespace, name string) *corev1.ServiceAccount {
	var sa *corev1.ServiceAccount
	err := wait.Poll(5*time.Second, time.Minute, func() (bool, error) {
		sa, err := client.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if len(sa.Secrets) > 0 {
			return true, nil
		}

		return false, nil

	})

	require.NoError(t, err)
	return sa
}
