package e2e

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/scoped"
)

// TestScopedClient ensures that we we can create a scoped client bound to a
// service account and then we can use the scoped client to make API calls.
func TestScopedClient(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, false)

	require.NotEmpty(t, kubeConfigPath)

	config, err := clientcmd.BuildConfigFromFlags("", *kubeConfigPath)
	require.NoError(t, err)

	kubeclient := newKubeClient(t)
	crclient := newCRClient(t)
	dynamicclient := newDynamicClient(t, config)

	logger := logrus.New()

	tests := []struct {
		name       string
		grant      func(namespace, name string) (cleanup cleanupFunc)
		assertFunc func(errGot error)
	}{
		// The parent test invokes 'Get' API on non existent objects. If the
		// scoped client has enough permission, we expect a NotFound error code.
		// Otherwise, we expect a 'Forbidden' error code due to lack of permission.
		{
			// The service account does not have any permission granted to it.
			// We expect the get api call to return 'Forbidden' error due to
			// lack of permission.
			name: "ServiceAccountDoesNotHaveAnyPermission",
			assertFunc: func(errGot error) {
				require.True(t, k8serrors.IsForbidden(errGot))
			},
		},
		{
			// The service account does have permission granted to it.
			// We expect the get api call to return 'NotFound' error.
			name: "ServiceAccountHasPermission",
			grant: func(namespace, name string) (cleanup cleanupFunc) {
				cleanup = grantPermission(t, kubeclient, namespace, name)
				return
			},
			assertFunc: func(errGot error) {
				require.True(t, k8serrors.IsNotFound(errGot))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Steps:
			// 1. Create a new namespace
			// 2. Create a service account.
			// 3. Grant permission(s) to the service account if specified.
			// 4. Get scoped client instance(s)
			// 5. Invoke Get API call on non existent object(s) to check if
			//    the call can be made successfully.
			namespace := genName("a")
			_, cleanupNS := newNamespace(t, kubeclient, namespace)
			defer cleanupNS()

			saName := genName("user-defined-")
			sa, cleanupSA := newServiceAccount(t, kubeclient, namespace, saName)
			defer cleanupSA()

			waitForServiceAccountSecretAvailable(t, kubeclient, sa.GetNamespace(), sa.GetName())

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
			require.NoError(t, errGot)
			require.NotNil(t, kubeclientGot)
			require.NotNil(t, crclientGot)

			_, errGot = kubeclientGot.KubernetesInterface().CoreV1().ConfigMaps(namespace).Get(genName("does-not-exist-"), metav1.GetOptions{})
			require.Error(t, errGot)
			tt.assertFunc(errGot)

			_, errGot = crclientGot.OperatorsV1alpha1().CatalogSources(namespace).Get(genName("does-not-exist-"), metav1.GetOptions{})
			require.Error(t, errGot)
			tt.assertFunc(errGot)

			gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "ConfigMap"}
			_, errGot = dynamicClientGot.Resource(gvr).Namespace(namespace).Get(genName("does-not-exist-"), metav1.GetOptions{})
			require.Error(t, errGot)
			tt.assertFunc(errGot)
		})
	}
}

func waitForServiceAccountSecretAvailable(t *testing.T, client operatorclient.ClientInterface, namespace, name string) *corev1.ServiceAccount {
	var sa *corev1.ServiceAccount
	err := wait.Poll(5*time.Second, time.Minute, func() (bool, error) {
		sa, err := client.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Get(name, metav1.GetOptions{})
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
