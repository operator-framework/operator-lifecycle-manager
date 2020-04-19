package wrappers

import (
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	listerfakes "github.com/operator-framework/operator-lifecycle-manager/pkg/fakes/client-go/listers"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient/operatorclientmocks"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes"
)

var (
	Controller         = false
	BlockOwnerDeletion = false
	WakeupInterval     = 5 * time.Second
)

func ownerReferenceFromCSV(csv *v1alpha1.ClusterServiceVersion) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         v1alpha1.SchemeGroupVersion.String(),
		Kind:               v1alpha1.ClusterServiceVersionKind,
		Name:               csv.GetName(),
		UID:                csv.GetUID(),
		Controller:         &Controller,
		BlockOwnerDeletion: &BlockOwnerDeletion,
	}
}

func TestEnsureServiceAccount(t *testing.T) {
	testErr := errors.New("NaNaNaNaN") // used to ensure exact error returned
	mockOwner := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "csv-owner",
			Namespace: "test-namespace",
		},
	}
	type state struct {
		namespace                  string
		existingServiceAccount     *corev1.ServiceAccount
		getServiceAccountError     error
		createServiceAccountResult *corev1.ServiceAccount
		createServiceAccountError  error
		updateServiceAccountResult *corev1.ServiceAccount
		updateServiceAccountError  error
	}
	type input struct {
		serviceAccountName     string
		serviceAccount         *corev1.ServiceAccount
		serviceAccountToUpdate *corev1.ServiceAccount
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
			name:    "ServiceAccount already exists, owned by CSV",
			subname: "returns existing SA when successfully fetched via Kubernetes API",
			state: state{
				namespace: "test-namespace",
				existingServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "existing-service-account-found",
						},
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
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
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
						},
					},
				},
				returnedError: nil,
			},
		},
		{
			name:    "ServiceAccount already exists, not owned by CSV",
			subname: "returns existing SA when successfully fetched via Kubernetes API",
			state: state{
				namespace: "test-namespace",
				existingServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service-account",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"test": "existing-service-account-found",
						},
					},
				},
				updateServiceAccountResult: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						Labels: map[string]string{
							"test": "existing-service-account-found",
						},
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
						},
					},
				},
				getServiceAccountError:    nil,
				createServiceAccountError: nil,
				updateServiceAccountError: nil,
			},
			input: input{
				serviceAccountName: "test-service-account",
				serviceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
					},
				},
				serviceAccountToUpdate: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service-account",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"test": "existing-service-account-found",
						},
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
						},
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
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
						},
					},
				},
				returnedError: nil,
			},
		},
		{
			name:    "ServiceAccount already exists, not owned by CSV, update fails",
			subname: "returns existing SA when successfully fetched via Kubernetes API",
			state: state{
				namespace: "test-namespace",
				existingServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service-account",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"test": "existing-service-account-found",
						},
					},
				},
				updateServiceAccountResult: nil,
				getServiceAccountError:     nil,
				createServiceAccountError:  nil,
				updateServiceAccountError:  testErr,
			},
			input: input{
				serviceAccountName: "test-service-account",
				serviceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
					},
				},
				serviceAccountToUpdate: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service-account",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"test": "existing-service-account-found",
						},
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
						},
					},
				},
			},
			expect: expect{
				returnedServiceAccount: nil,
				returnedError:          testErr,
			},
		},
		{
			name:    "ServiceAccount already exists",
			subname: "returns SA unmodified when fails to create it due to it already existing",
			state: state{
				namespace: "test-namespace",
				existingServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service-account",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"test": "existing-service-account-create-conflict",
						},
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
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
						Name:      "test-service-account",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"test": "existing-service-account-create-conflict",
						},
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
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
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
						},
					},
				},
				createServiceAccountError: nil,
				getServiceAccountError:    apierrors.NewNotFound(corev1.Resource("serviceaccounts"), "test-service-account"),
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
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
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
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
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
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
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
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
						},
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
				namespace: "test-namespace",
				getServiceAccountError: apierrors.NewNotFound(
					corev1.Resource("serviceaccounts"), "test-service-account"),
				createServiceAccountError: testErr,
			},
			input: input{
				serviceAccountName: "test-service-account",
				serviceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-account",
						OwnerReferences: []metav1.OwnerReference{
							ownerReferenceFromCSV(&mockOwner),
						},
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
			mockOpClient := operatorclientmocks.NewMockClientInterface(ctrl)
			fakeLister := &operatorlisterfakes.FakeOperatorLister{}
			fakeCoreV1Lister := &operatorlisterfakes.FakeCoreV1Lister{}
			fakeServiceAccountLister := &listerfakes.FakeServiceAccountLister{}
			fakeServiceAccountNamespacedLister := &listerfakes.FakeServiceAccountNamespaceLister{}
			fakeServiceAccountNamespacedLister.GetReturns(tt.state.existingServiceAccount, tt.state.getServiceAccountError)
			fakeServiceAccountLister.ServiceAccountsReturns(fakeServiceAccountNamespacedLister)
			fakeCoreV1Lister.ServiceAccountListerReturns(fakeServiceAccountLister)
			fakeLister.CoreV1Returns(fakeCoreV1Lister)

			client := NewInstallStrategyDeploymentClient(mockOpClient, fakeLister, tt.state.namespace)

			mockOpClient.EXPECT().
				CreateServiceAccount(tt.input.serviceAccount).
				Return(tt.state.createServiceAccountResult, tt.state.createServiceAccountError).
				AnyTimes()

			mockOpClient.EXPECT().
				UpdateServiceAccount(tt.input.serviceAccountToUpdate).
				Return(tt.state.updateServiceAccountResult, tt.state.updateServiceAccountError).
				AnyTimes()

			sa, err := client.EnsureServiceAccount(tt.input.serviceAccount, &mockOwner)

			require.True(t, equality.Semantic.DeepEqual(tt.expect.returnedServiceAccount, sa),
				"Resources do not match <expected, actual>: %s",
				diff.ObjectDiff(tt.expect.returnedServiceAccount, sa))

			require.EqualValues(t, tt.expect.returnedError, errors.Cause(err))

			ctrl.Finish()
		})
	}
}
