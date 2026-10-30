package install

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/wrappers"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes"
)

const (
	testCRDName   = "tests.example.com"
	testNamespace = "test-namespace"
	testCSVName   = "test-operator.v1.0.0"
)

// singletonCSV returns a CSV that satisfies isSingletonOperator (AllNamespaces only).
func singletonCSV(crdName string) *operatorsv1alpha1.ClusterServiceVersion {
	return &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testCSVName,
			Namespace: testNamespace,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			InstallModes: []operatorsv1alpha1.InstallMode{
				{Type: operatorsv1alpha1.InstallModeTypeAllNamespaces, Supported: true},
				{Type: operatorsv1alpha1.InstallModeTypeSingleNamespace, Supported: false},
				{Type: operatorsv1alpha1.InstallModeTypeMultiNamespace, Supported: false},
				{Type: operatorsv1alpha1.InstallModeTypeOwnNamespace, Supported: false},
			},
			CustomResourceDefinitions: operatorsv1alpha1.CustomResourceDefinitions{
				Owned: []operatorsv1alpha1.CRDDescription{
					{Name: crdName, Version: "v2", Kind: "Test"},
				},
			},
		},
	}
}

// conversionDesc returns a WebhookDescription of type ConversionWebhook for crdName.
func conversionDesc(crdName string) operatorsv1alpha1.WebhookDescription {
	port := int32(443)
	return operatorsv1alpha1.WebhookDescription{
		Type:                    operatorsv1alpha1.ConversionWebhook,
		GenerateName:            "test-webhook",
		ConversionCRDs:          []string{crdName},
		AdmissionReviewVersions: []string{"v1"},
		ContainerPort:           port,
	}
}

// makeInstaller builds a StrategyDeploymentInstaller backed by a fake apiextensions client
// that has crd pre-loaded. Returns the installer and the fake client for post-call assertions.
func makeInstaller(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) (*StrategyDeploymentInstaller, *apiextensionsfake.Clientset) {
	t.Helper()
	crdFake := apiextensionsfake.NewSimpleClientset(crd)
	opClient := operatorclient.NewClient(
		k8sfake.NewSimpleClientset(),
		crdFake,
		apiregistrationfake.NewSimpleClientset(),
	)
	fakeLister := &operatorlisterfakes.FakeOperatorLister{}
	strategyClient := wrappers.NewInstallStrategyDeploymentClient(opClient, fakeLister, testNamespace)
	installer := &StrategyDeploymentInstaller{
		strategyClient: strategyClient,
		owner:          singletonCSV(crd.Name),
	}
	return installer, crdFake
}

// crdWithVersions builds a minimal CRD with the given spec versions and optional annotations.
func crdWithVersions(annotations map[string]string, versions ...apiextensionsv1.CustomResourceDefinitionVersion) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testCRDName,
			Annotations: annotations,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "tests",
				Kind:   "Test",
			},
			Scope:    apiextensionsv1.ClusterScoped,
			Versions: versions,
		},
	}
}

func crdVersion(name string, storage bool) apiextensionsv1.CustomResourceDefinitionVersion {
	return apiextensionsv1.CustomResourceDefinitionVersion{
		Name:    name,
		Served:  true,
		Storage: storage,
	}
}

// TestConversionWebhook_PromotesPendingStorageVersion verifies that when the
// PendingStorageVersionAnnotation is set to a version that exists in the CRD spec,
// createOrUpdateConversionWebhook promotes that version to storage=true, clears the
// annotation, and sets the conversion webhook — all in one Update call.
func TestConversionWebhook_PromotesPendingStorageVersion(t *testing.T) {
	crd := crdWithVersions(
		map[string]string{PendingStorageVersionAnnotation: "v3"},
		crdVersion("v2", true),
		crdVersion("v3", false),
	)
	installer, crdFake := makeInstaller(t, crd)

	err := installer.createOrUpdateConversionWebhook([]byte("fake-ca"), conversionDesc(testCRDName))
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), testCRDName, metav1.GetOptions{})
	require.NoError(t, err)

	for _, v := range updated.Spec.Versions {
		if v.Name == "v3" {
			assert.True(t, v.Storage, "v3 should be the storage version after promotion")
		} else {
			assert.False(t, v.Storage, "v2 should no longer be the storage version after promotion")
		}
	}
	assert.NotContains(t, updated.Annotations, PendingStorageVersionAnnotation, "annotation should be cleared after promotion")
	require.NotNil(t, updated.Spec.Conversion, "conversion webhook should be set")
	assert.Equal(t, apiextensionsv1.WebhookConverter, updated.Spec.Conversion.Strategy)
}

// TestConversionWebhook_NoAnnotation verifies that when no PendingStorageVersionAnnotation
// is present, storage markers are unchanged and only the conversion webhook is set.
func TestConversionWebhook_NoAnnotation(t *testing.T) {
	crd := crdWithVersions(
		nil,
		crdVersion("v2", true),
		crdVersion("v3", false),
	)
	installer, crdFake := makeInstaller(t, crd)

	err := installer.createOrUpdateConversionWebhook([]byte("fake-ca"), conversionDesc(testCRDName))
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), testCRDName, metav1.GetOptions{})
	require.NoError(t, err)

	for _, v := range updated.Spec.Versions {
		if v.Name == "v2" {
			assert.True(t, v.Storage, "v2 should remain the storage version")
		} else {
			assert.False(t, v.Storage, "v3 should remain non-storage")
		}
	}
	require.NotNil(t, updated.Spec.Conversion)
	assert.Equal(t, apiextensionsv1.WebhookConverter, updated.Spec.Conversion.Strategy)
}

// TestConversionWebhook_StaleAnnotationVersionNotInSpec verifies that when the
// PendingStorageVersionAnnotation names a version that does not exist in the CRD spec,
// storage markers are left untouched, the annotation is cleared, and the conversion
// webhook is still set successfully.
func TestConversionWebhook_StaleAnnotationVersionNotInSpec(t *testing.T) {
	crd := crdWithVersions(
		map[string]string{PendingStorageVersionAnnotation: "v99"},
		crdVersion("v2", true),
		crdVersion("v3", false),
	)
	installer, crdFake := makeInstaller(t, crd)

	err := installer.createOrUpdateConversionWebhook([]byte("fake-ca"), conversionDesc(testCRDName))
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), testCRDName, metav1.GetOptions{})
	require.NoError(t, err)

	for _, v := range updated.Spec.Versions {
		if v.Name == "v2" {
			assert.True(t, v.Storage, "v2 should remain storage when annotation version is absent from spec")
		} else {
			assert.False(t, v.Storage)
		}
	}
	assert.NotContains(t, updated.Annotations, PendingStorageVersionAnnotation,
		"stale annotation should be cleared after warning so future reconciles do not loop indefinitely")
	require.NotNil(t, updated.Spec.Conversion)
	assert.Equal(t, apiextensionsv1.WebhookConverter, updated.Spec.Conversion.Strategy)
}

// TestConversionWebhook_EmptyAnnotationSkipsPromotion verifies that an empty-string
// PendingStorageVersionAnnotation does not trigger the promotion block.
func TestConversionWebhook_EmptyAnnotationSkipsPromotion(t *testing.T) {
	crd := crdWithVersions(
		map[string]string{PendingStorageVersionAnnotation: ""},
		crdVersion("v2", true),
	)
	installer, crdFake := makeInstaller(t, crd)

	err := installer.createOrUpdateConversionWebhook([]byte("fake-ca"), conversionDesc(testCRDName))
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), testCRDName, metav1.GetOptions{})
	require.NoError(t, err)

	require.Len(t, updated.Spec.Versions, 1)
	assert.True(t, updated.Spec.Versions[0].Storage, "storage marker should be unchanged when annotation is empty")
	require.NotNil(t, updated.Spec.Conversion)
	assert.Equal(t, apiextensionsv1.WebhookConverter, updated.Spec.Conversion.Strategy)
}
