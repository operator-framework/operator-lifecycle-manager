package olm

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func testWebhookOwnerCSV(generateNames ...string) *v1alpha1.ClusterServiceVersion {
	csv := &v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-csv",
			Namespace: "test-ns",
			UID:       types.UID("test-csv-uid"),
		},
	}
	for _, name := range generateNames {
		csv.Spec.WebhookDefinitions = append(csv.Spec.WebhookDefinitions, v1alpha1.WebhookDescription{
			GenerateName: name,
		})
	}
	return csv
}

func testWebhookLabels(csv *v1alpha1.ClusterServiceVersion, generateName string) map[string]string {
	webhookLabels := ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)
	webhookLabels[install.WebhookDescKey] = generateName
	return webhookLabels
}

func testValidatingWebhookConfiguration(csv *v1alpha1.ClusterServiceVersion, name, generateName string) *admissionregistrationv1.ValidatingWebhookConfiguration {
	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: testWebhookLabels(csv, generateName),
		},
	}
}

func testMutatingWebhookConfiguration(csv *v1alpha1.ClusterServiceVersion, name, generateName string) *admissionregistrationv1.MutatingWebhookConfiguration {
	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: testWebhookLabels(csv, generateName),
		},
	}
}

func TestCleanUpRemovedWebhooks(t *testing.T) {
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: "admissionregistration.k8s.io", Resource: "webhookconfigurations"},
		"stale",
		errors.New("webhooks are locked"),
	)
	notFound := apierrors.NewNotFound(
		schema.GroupResource{Group: "admissionregistration.k8s.io", Resource: "webhookconfigurations"},
		"stale",
	)

	tests := []struct {
		name                   string
		csv                    *v1alpha1.ClusterServiceVersion
		validatingWebhooks     []*admissionregistrationv1.ValidatingWebhookConfiguration
		mutatingWebhooks       []*admissionregistrationv1.MutatingWebhookConfiguration
		validatingDeleteError  error
		mutatingDeleteError    error
		wantError              string
		wantValidatingWebhooks []string
		wantMutatingWebhooks   []string
	}{
		{
			name: "deletes stale validating and mutating webhook configurations",
			csv:  testWebhookOwnerCSV("kept"),
			validatingWebhooks: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				testValidatingWebhookConfiguration(testWebhookOwnerCSV("kept"), "stale-validating", "stale"),
			},
			mutatingWebhooks: []*admissionregistrationv1.MutatingWebhookConfiguration{
				testMutatingWebhookConfiguration(testWebhookOwnerCSV("kept"), "stale-mutating", "stale"),
			},
			wantValidatingWebhooks: []string{},
			wantMutatingWebhooks:   []string{},
		},
		{
			name: "keeps webhook configurations still referenced by the csv",
			csv:  testWebhookOwnerCSV("kept"),
			validatingWebhooks: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				testValidatingWebhookConfiguration(testWebhookOwnerCSV("kept"), "kept-validating", "kept"),
			},
			mutatingWebhooks: []*admissionregistrationv1.MutatingWebhookConfiguration{
				testMutatingWebhookConfiguration(testWebhookOwnerCSV("kept"), "kept-mutating", "kept"),
			},
			wantValidatingWebhooks: []string{"kept-validating"},
			wantMutatingWebhooks:   []string{"kept-mutating"},
		},
		{
			name: "returns validating delete failures instead of swallowing them",
			csv:  testWebhookOwnerCSV(),
			validatingWebhooks: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				testValidatingWebhookConfiguration(testWebhookOwnerCSV(), "stale-validating", "stale"),
			},
			validatingDeleteError:  forbidden,
			wantError:              "webhooks are locked",
			wantValidatingWebhooks: []string{"stale-validating"},
		},
		{
			name: "returns mutating delete failures instead of swallowing them",
			csv:  testWebhookOwnerCSV(),
			mutatingWebhooks: []*admissionregistrationv1.MutatingWebhookConfiguration{
				testMutatingWebhookConfiguration(testWebhookOwnerCSV(), "stale-mutating", "stale"),
			},
			mutatingDeleteError:    forbidden,
			wantError:              "webhooks are locked",
			wantMutatingWebhooks:   []string{"stale-mutating"},
			wantValidatingWebhooks: []string{},
		},
		{
			name: "treats already deleted validating webhook configurations as success",
			csv:  testWebhookOwnerCSV(),
			validatingWebhooks: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				testValidatingWebhookConfiguration(testWebhookOwnerCSV(), "stale-validating", "stale"),
			},
			validatingDeleteError:  notFound,
			wantValidatingWebhooks: []string{"stale-validating"},
			wantMutatingWebhooks:   []string{},
		},
		{
			name: "treats already deleted mutating webhook configurations as success",
			csv:  testWebhookOwnerCSV(),
			mutatingWebhooks: []*admissionregistrationv1.MutatingWebhookConfiguration{
				testMutatingWebhookConfiguration(testWebhookOwnerCSV(), "stale-mutating", "stale"),
			},
			mutatingDeleteError:    notFound,
			wantMutatingWebhooks:   []string{"stale-mutating"},
			wantValidatingWebhooks: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var k8sObjects []runtime.Object
			for _, webhook := range tt.validatingWebhooks {
				k8sObjects = append(k8sObjects, webhook)
			}
			for _, webhook := range tt.mutatingWebhooks {
				k8sObjects = append(k8sObjects, webhook)
			}

			k8sClient := k8sfake.NewSimpleClientset(k8sObjects...)
			if tt.validatingDeleteError != nil {
				k8sClient.PrependReactor("delete", "validatingwebhookconfigurations", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, tt.validatingDeleteError
				})
			}
			if tt.mutatingDeleteError != nil {
				k8sClient.PrependReactor("delete", "mutatingwebhookconfigurations", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, tt.mutatingDeleteError
				})
			}

			op := &Operator{
				opClient: operatorclient.NewClient(
					k8sClient,
					apiextensionsfake.NewSimpleClientset(),
					apiregistrationfake.NewSimpleClientset(),
				),
			}

			err := op.cleanUpRemovedWebhooks(tt.csv)
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}

			validatingList, err := k8sClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{})
			require.NoError(t, err)
			gotValidating := make([]string, 0, len(validatingList.Items))
			for i := range validatingList.Items {
				gotValidating = append(gotValidating, validatingList.Items[i].Name)
			}
			require.ElementsMatch(t, tt.wantValidatingWebhooks, gotValidating)

			mutatingList, err := k8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{})
			require.NoError(t, err)
			gotMutating := make([]string, 0, len(mutatingList.Items))
			for i := range mutatingList.Items {
				gotMutating = append(gotMutating, mutatingList.Items[i].Name)
			}
			require.ElementsMatch(t, tt.wantMutatingWebhooks, gotMutating)
		})
	}
}
