package client

import (
	"fmt"
	"testing"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestEnsureServiceAccount(t *testing.T) {
	testErr := errors.New("NaNaNaNaN") // used to ensure exact error returned

	type state struct {
		namespace                  string
		existingServiceAccount     *corev1.ServiceAccount
		getServiceAccountError     error
		createServiceAccountResult *corev1.ServiceAccount
		createServiceAccountError  error
	}
	type input struct {
		serviceAccountName string
		serviceAccount     *corev1.ServiceAccount
	}
	type expect struct {
		returnedServiceAccount *corev1.ServiceAccount
		returnedError          error
	}

	tests := []struct {
		name    string
		subname string
		state   state
		input   input
		expect  expect
	}{
		{
			name:    "Bad ServiceAccount",
			subname: "nil value",
			expect: expect{
				returnedError: ErrNilObject,
			},
		},
		{
			name:    "ServiceAccount already exists",
			subname: "returns existing SA when successfully fetched via Kubernetes API",
			state: state{
				namespace: "test-namespace",
				existingServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "existing-service-account-found",
						},
					},
				},
				getServiceAccountError:    nil,
				createServiceAccountError: nil,
			},
			input: input{
				serviceAccountName: "test-service-account",
				serviceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
					},
				},
			},
			expect: expect{
				returnedServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "existing-service-account-found",
						},
					},
				},
				returnedError: nil,
			},
		},
		{
			name:    "ServiceAccount already exists",
			subname: "returns SA unmodified when fails to create it due to it already existing",
			state: state{
				namespace: "test-namespace",
				existingServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "existing-service-account-create-conflict",
						},
					},
				},
				getServiceAccountError: nil,
				createServiceAccountError: apierrors.NewAlreadyExists(
					corev1.Resource("serviceaccounts"), "test-service-account"),
			},
			input: input{
				serviceAccountName: "test-service-account",
				serviceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
					},
				},
			},
			expect: expect{
				returnedServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "existing-service-account-create-conflict",
						},
					},
				},
				returnedError: nil,
			},
		},
		{
			name:    "ServiceAccount doesn't already exist",
			subname: "creates SA when no errors or existing SAs found",
			state: state{
				namespace: "test-namespace",
				createServiceAccountResult: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "successfully-created-serviceaccount",
						},
					},
				},
				createServiceAccountError: nil,
			},
			input: input{
				serviceAccountName: "test-service-account",
				serviceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
					},
				},
			},
			expect: expect{
				returnedServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "successfully-created-serviceaccount",
						},
					},
				},
				returnedError: nil,
			},
		},
		{
			name:    "ServiceAccount doesn't already exist",
			subname: "creates SA successfully after getting NotFound error trying to fetch it",
			state: state{
				namespace: "test-namespace",
				getServiceAccountError: apierrors.NewNotFound(
					corev1.Resource("serviceaccounts"), "test-service-account"),
				createServiceAccountResult: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "successfully-created-serviceaccount-notfound-error",
						},
					},
				},
				createServiceAccountError: nil,
			},
			input: input{
				serviceAccountName: "test-service-account",
				serviceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
					},
				},
			},
			expect: expect{
				returnedServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "successfully-created-serviceaccount-notfound-error",
						},
					},
				},
				returnedError: nil,
			},
		},
		{
			name:    "Unknown errors",
			subname: "returns unknown errors received trying to fetch SA from the kubernetes API",
			state: state{
				namespace:                 "test-namespace",
				getServiceAccountError:    testErr,
				createServiceAccountError: nil,
			},
			input: input{
				serviceAccountName: "test-service-account",
				serviceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
					},
				},
			},
			expect: expect{
				returnedError: testErr,
			},
		},
		{
			name:    "Unknown errors",
			subname: "returns unknown errors received trying to create SA",
			state: state{
				namespace:                 "test-namespace",
				getServiceAccountError:    nil,
				createServiceAccountError: testErr,
			},
			input: input{
				serviceAccountName: "test-service-account",
				serviceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
					},
				},
			},
			expect: expect{
				returnedError: testErr,
			},
		},
	}

	for _, tt := range tests {
		testName := fmt.Sprintf("%s: %s", tt.name, tt.subname)
		t.Run(testName, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockOpClient := opClient.NewMockInterface(ctrl)
			client := NewInstallStrategyDeploymentClient(mockOpClient, tt.state.namespace)

			mockOpClient.EXPECT().
				GetServiceAccount(tt.state.namespace, tt.input.serviceAccountName).
				Return(tt.state.existingServiceAccount, tt.state.getServiceAccountError).
				AnyTimes()

			mockOpClient.EXPECT().
				CreateServiceAccount(tt.input.serviceAccount).
				Return(tt.state.createServiceAccountResult, tt.state.createServiceAccountError).
				AnyTimes()

			sa, err := client.EnsureServiceAccount(tt.input.serviceAccount)

			require.True(t, equality.Semantic.DeepEqual(tt.expect.returnedServiceAccount, sa),
				"Resources do not match <expected, actual>: %s",
				diff.ObjectDiff(tt.expect.returnedServiceAccount, sa))

			require.EqualValues(t, tt.expect.returnedError, errors.Cause(err))

			ctrl.Finish()
		})
	}
}
