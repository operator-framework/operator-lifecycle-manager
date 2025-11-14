package catalog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient/operatorclientmocks"
)

func TestMergedOwnerReferences(t *testing.T) {
	var (
		True  = true
		False = false
	)

	for _, tc := range []struct {
		Name string
		In   [][]metav1.OwnerReference
		Out  []metav1.OwnerReference
	}{
		{
			Name: "empty",
		},
		{
			Name: "different uid",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &True,
						BlockOwnerDeletion: &True,
						UID:                "x",
					},
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &True,
						BlockOwnerDeletion: &True,
						UID:                "y",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &True,
					BlockOwnerDeletion: &True,
					UID:                "x",
				},
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &True,
					BlockOwnerDeletion: &True,
					UID:                "y",
				},
			},
		},
		{
			Name: "different controller",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &True,
						BlockOwnerDeletion: &True,
						UID:                "x",
					},
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &False,
						BlockOwnerDeletion: &True,
						UID:                "x",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &True,
					BlockOwnerDeletion: &True,
					UID:                "x",
				},
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &False,
					BlockOwnerDeletion: &True,
					UID:                "x",
				},
			},
		},
		{
			Name: "add owner without uid",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c-1",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "x",
					},
				},
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c-2",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c-1",
					Controller:         &False,
					BlockOwnerDeletion: &False,
					UID:                "x",
				},
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c-2",
					Controller:         &False,
					BlockOwnerDeletion: &False,
					UID:                "",
				},
			},
		},
		{
			Name: "duplicates combined",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "x",
					},
				},
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "x",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &False,
					BlockOwnerDeletion: &False,
					UID:                "x",
				},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.Out, mergedOwnerReferences(tc.In...))
		})
	}
}

func TestEnsureServiceAccount(t *testing.T) {
	namespace := "test-namespace"
	saName := "test-sa"

	tests := []struct {
		name                   string
		existingServiceAccount *corev1.ServiceAccount
		newServiceAccount      *corev1.ServiceAccount
		expectedAnnotations    map[string]string
		expectedStatus         v1alpha1.StepStatus
		expectError            bool
		createError            error
		getError               error
		updateError            error
	}{
		{
			name: "create new service account",
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
					Annotations: map[string]string{
						"new-annotation": "new-value",
					},
				},
			},
			expectedAnnotations: map[string]string{
				"new-annotation": "new-value",
			},
			expectedStatus: v1alpha1.StepStatusCreated,
		},
		{
			name: "update existing service account - preserve existing annotations",
			existingServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
					Annotations: map[string]string{
						"existing-annotation": "existing-value",
						"override-annotation": "old-value",
					},
				},
				Secrets: []corev1.ObjectReference{
					{Name: "existing-secret"},
				},
			},
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
					Annotations: map[string]string{
						"new-annotation":      "new-value",
						"override-annotation": "new-value",
					},
				},
			},
			expectedAnnotations: map[string]string{
				"existing-annotation": "existing-value",
				"new-annotation":      "new-value",
				"override-annotation": "new-value",
			},
			expectedStatus: v1alpha1.StepStatusPresent,
			createError:    apierrors.NewAlreadyExists(corev1.Resource("serviceaccounts"), saName),
		},
		{
			name: "update existing service account - no annotations on new SA",
			existingServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
					Annotations: map[string]string{
						"existing-annotation": "existing-value",
					},
				},
			},
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			expectedAnnotations: map[string]string{
				"existing-annotation": "existing-value",
			},
			expectedStatus: v1alpha1.StepStatusPresent,
			createError:    apierrors.NewAlreadyExists(corev1.Resource("serviceaccounts"), saName),
		},
		{
			name: "update existing service account - preserve secrets",
			existingServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
				Secrets: []corev1.ObjectReference{
					{Name: "secret-1"},
					{Name: "secret-2"},
				},
			},
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			expectedAnnotations: map[string]string{},
			expectedStatus:      v1alpha1.StepStatusPresent,
			createError:         apierrors.NewAlreadyExists(corev1.Resource("serviceaccounts"), saName),
		},
		{
			name: "create error - not already exists",
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			createError: apierrors.NewInternalError(assert.AnError),
			expectError: true,
		},
		{
			name: "update error - get existing fails",
			existingServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			newServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: namespace,
				},
			},
			createError: apierrors.NewAlreadyExists(corev1.Resource("serviceaccounts"), saName),
			getError:    apierrors.NewInternalError(assert.AnError),
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Create mock client
			mockClient := operatorclientmocks.NewMockClientInterface(ctrl)

			// Create fake kubernetes client
			var objects []runtime.Object
			if tc.existingServiceAccount != nil {
				objects = append(objects, tc.existingServiceAccount)
			}

			fakeClient := k8sfake.NewSimpleClientset(objects...)

			// Setup expectations
			mockClient.EXPECT().KubernetesInterface().Return(fakeClient).AnyTimes()

			// Mock the create call
			if tc.createError != nil {
				// We need to intercept the create call and return the error
				fakeClient.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.createError
				})
			}

			// Mock the get call if needed
			if tc.getError != nil {
				fakeClient.PrependReactor("get", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.getError
				})
			}

			// Mock UpdateServiceAccount if the test expects an update
			if tc.createError != nil && apierrors.IsAlreadyExists(tc.createError) && tc.getError == nil {
				// Calculate expected SA after merge
				expectedSA := tc.newServiceAccount.DeepCopy()
				if tc.existingServiceAccount != nil {
					expectedSA.Secrets = tc.existingServiceAccount.Secrets
					// Merge annotations
					if expectedSA.Annotations == nil {
						expectedSA.Annotations = make(map[string]string)
					}
					for k, v := range tc.existingServiceAccount.Annotations {
						if _, ok := expectedSA.Annotations[k]; !ok {
							expectedSA.Annotations[k] = v
						}
					}
				}
				expectedSA.SetNamespace(namespace)

				mockClient.EXPECT().UpdateServiceAccount(gomock.Any()).DoAndReturn(func(sa *corev1.ServiceAccount) (*corev1.ServiceAccount, error) {
					// Verify the merged service account has the expected annotations
					assert.Equal(t, tc.expectedAnnotations, sa.Annotations)
					// Verify secrets were preserved if they existed
					if tc.existingServiceAccount != nil && len(tc.existingServiceAccount.Secrets) > 0 {
						assert.Equal(t, tc.existingServiceAccount.Secrets, sa.Secrets)
					}
					return sa, tc.updateError
				}).MaxTimes(1)
			}

			// Create StepEnsurer
			ensurer := &StepEnsurer{
				kubeClient:    mockClient,
				crClient:      fake.NewSimpleClientset(),
				dynamicClient: fakedynamic.NewSimpleDynamicClient(runtime.NewScheme()),
			}

			// Execute EnsureServiceAccount
			status, err := ensurer.EnsureServiceAccount(namespace, tc.newServiceAccount)

			// Verify results
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedStatus, status)
			}
		})
	}
}
