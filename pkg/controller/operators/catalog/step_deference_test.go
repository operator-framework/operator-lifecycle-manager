package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes"
)

// staticResolver implements ManifestResolver and always returns the same manifest.
type staticResolver struct{ manifest string }

func (r staticResolver) ManifestForStep(_ *operatorsv1alpha1.Step) (string, error) {
	return r.manifest, nil
}

// minimalSchema returns a bare-minimum schema accepted by the CRD API.
func minimalSchema() *apiextensionsv1.CustomResourceValidation {
	return &apiextensionsv1.CustomResourceValidation{
		OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
	}
}

// buildCRD builds a CRD with the given versions and optional annotations.
func buildCRD(name string, annotations map[string]string, versions ...apiextensionsv1.CustomResourceDefinitionVersion) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiextensions.k8s.io/v1",
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
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

func storedVersion(name string, storage bool) apiextensionsv1.CustomResourceDefinitionVersion {
	return apiextensionsv1.CustomResourceDefinitionVersion{
		Name:    name,
		Served:  true,
		Storage: storage,
		Schema:  minimalSchema(),
	}
}

// crdManifest JSON-encodes a CRD so it can be used as a step manifest.
func crdManifest(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) string {
	t.Helper()
	b, err := json.Marshal(crd)
	require.NoError(t, err)
	return string(b)
}

// csvManifestWithConversionWebhook JSON-encodes a CSV that declares a ConversionWebhook for crdName.
func csvManifestWithConversionWebhook(t *testing.T, csvName, crdName string) string {
	t.Helper()
	csv := &operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "operators.coreos.com/v1alpha1",
			Kind:       "ClusterServiceVersion",
		},
		ObjectMeta: metav1.ObjectMeta{Name: csvName},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			WebhookDefinitions: []operatorsv1alpha1.WebhookDescription{
				{
					Type:           operatorsv1alpha1.ConversionWebhook,
					GenerateName:   "test-webhook",
					ConversionCRDs: []string{crdName},
				},
			},
		},
	}
	b, err := json.Marshal(csv)
	require.NoError(t, err)
	return string(b)
}

// buildBuilder creates a builder wired with a fake apiextensions client that has
// currentCRD pre-loaded, and an InstallPlan whose Status.Plan contains csvManifest.
func buildBuilder(t *testing.T, currentCRD *apiextensionsv1.CustomResourceDefinition, newCRD *apiextensionsv1.CustomResourceDefinition, csvName, csvManifest string) (*builder, *apiextensionsfake.Clientset) {
	t.Helper()

	crdFake := apiextensionsfake.NewSimpleClientset(currentCRD)
	opClient := operatorclient.NewClient(
		k8sfake.NewSimpleClientset(),
		crdFake,
		apiregistrationfake.NewSimpleClientset(),
	)
	// validateExistingCRs lists resources via the dynamic client; the fake client
	// panics unless every GVR it will list is registered. Register the CRD's
	// group/resource for all served versions so the list returns empty (no CRs exist).
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Group: currentCRD.Spec.Group, Version: "v2", Resource: currentCRD.Spec.Names.Plural}: "TestList",
		{Group: currentCRD.Spec.Group, Version: "v3", Resource: currentCRD.Spec.Names.Plural}: "TestList",
	}
	dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)
	fakeLister := &operatorlisterfakes.FakeOperatorLister{}
	csvFakeLister := &operatorlisterfakes.FakeClusterServiceVersionLister{}

	plan := &operatorsv1alpha1.InstallPlan{
		Status: operatorsv1alpha1.InstallPlanStatus{
			Plan: []*operatorsv1alpha1.Step{
				{
					Resource: operatorsv1alpha1.StepResource{
						Kind:     "ClusterServiceVersion",
						Name:     csvName,
						Manifest: csvManifest,
					},
				},
			},
		},
	}

	logger := logrus.NewEntry(logrus.New())
	recorder := record.NewFakeRecorder(10)
	_ = fakeLister

	b := newBuilder(plan, csvFakeLister, opClient, dynamicClient, staticResolver{manifest: crdManifest(t, newCRD)}, logger, recorder)
	return b, crdFake
}

// stepFor builds a Step for a CRD update (status Unknown = triggers the create/update path).
func stepFor(crdName, csvName string) operatorsv1alpha1.Step {
	return operatorsv1alpha1.Step{
		Status: operatorsv1alpha1.StepStatusUnknown,
		Resource: operatorsv1alpha1.StepResource{
			Kind:    crdKind,
			Name:    crdName,
			Version: "v1",
		},
		Resolving: csvName,
	}
}

// TestNewCRDV1Step_StripsBundleSuppliedPendingAnnotation verifies that when a bundle
// CRD arrives with PendingStorageVersionAnnotation already set in its manifest, the
// annotation is removed before the CRD is written to the cluster. Only the deference
// block may set this annotation; a bundle-supplied value must never reach the promotion
// block in createOrUpdateConversionWebhook.
func TestNewCRDV1Step_StripsBundleSuppliedPendingAnnotation(t *testing.T) {
	const crdName = "tests.example.com"
	const csvName = "operator.v1.0.0"

	currentCRD := buildCRD(crdName, nil, storedVersion("v2", true))
	currentCRD.Status.StoredVersions = []string{"v2"}

	// New bundle carries the same storage version (no promotion) but has the
	// reserved annotation pre-set in its manifest — simulating a malicious or
	// accidentally-annotated bundle.
	newCRD := buildCRD(crdName,
		map[string]string{install.PendingStorageVersionAnnotation: "v2"},
		storedVersion("v2", true),
	)

	b, crdFake := buildBuilder(t, currentCRD, newCRD, csvName, csvManifestWithConversionWebhook(t, csvName, crdName))
	step := stepFor(crdName, csvName)

	s, err := b.create(step)
	require.NoError(t, err)
	_, err = s.Status()
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.NotContains(t, updated.Annotations, install.PendingStorageVersionAnnotation,
		"bundle-supplied PendingStorageVersionAnnotation must be stripped before writing to the cluster")
}

// ── storageVersionInSpec ──────────────────────────────────────────────────────

func TestStorageVersionInSpec_ReturnsStorageVersion(t *testing.T) {
	crd := buildCRD("test.example.com", nil,
		storedVersion("v2", false),
		storedVersion("v3", true),
	)
	got, ok := storageVersionInSpec(crd)
	assert.True(t, ok)
	assert.Equal(t, "v3", got)
}

func TestStorageVersionInSpec_NoneStorageReturnsFalse(t *testing.T) {
	crd := buildCRD("test.example.com", nil,
		storedVersion("v2", false),
		storedVersion("v3", false),
	)
	got, ok := storageVersionInSpec(crd)
	assert.False(t, ok)
	assert.Empty(t, got)
}

func TestStorageVersionInSpec_SingleVersion(t *testing.T) {
	crd := buildCRD("test.example.com", nil, storedVersion("v1", true))
	got, ok := storageVersionInSpec(crd)
	assert.True(t, ok)
	assert.Equal(t, "v1", got)
}

// ── csvDeclaresConversionWebhookForCRD ────────────────────────────────────────

func TestCsvDeclaresConversionWebhook_ReturnsTrueWhenDeclared(t *testing.T) {
	csvName := "operator.v1.0.0"
	crdName := "tests.example.com"
	csvJSON := csvManifestWithConversionWebhook(t, csvName, crdName)

	plan := &operatorsv1alpha1.InstallPlan{
		Status: operatorsv1alpha1.InstallPlanStatus{
			Plan: []*operatorsv1alpha1.Step{
				{Resource: operatorsv1alpha1.StepResource{Kind: "ClusterServiceVersion", Name: csvName, Manifest: csvJSON}},
			},
		},
	}
	assert.True(t, csvDeclaresConversionWebhookForCRD(plan, csvName, crdName))
}

func TestCsvDeclaresConversionWebhook_ReturnsFalseWhenNotDeclared(t *testing.T) {
	csvName := "operator.v1.0.0"
	crdName := "tests.example.com"
	csvJSON := csvManifestWithConversionWebhook(t, csvName, "other.example.com")

	plan := &operatorsv1alpha1.InstallPlan{
		Status: operatorsv1alpha1.InstallPlanStatus{
			Plan: []*operatorsv1alpha1.Step{
				{Resource: operatorsv1alpha1.StepResource{Kind: "ClusterServiceVersion", Name: csvName, Manifest: csvJSON}},
			},
		},
	}
	assert.False(t, csvDeclaresConversionWebhookForCRD(plan, csvName, crdName))
}

func TestCsvDeclaresConversionWebhook_ReturnsFalseWhenCSVAbsentFromPlan(t *testing.T) {
	plan := &operatorsv1alpha1.InstallPlan{
		Status: operatorsv1alpha1.InstallPlanStatus{Plan: []*operatorsv1alpha1.Step{}},
	}
	assert.False(t, csvDeclaresConversionWebhookForCRD(plan, "missing-csv", "tests.example.com"))
}

// ── deference block in NewCRDV1Step ──────────────────────────────────────────

func TestNewCRDV1Step_DefersStorageVersionWhenNoConversionAndCSVHasWebhook(t *testing.T) {
	const crdName = "tests.example.com"
	const csvName = "operator.v1.0.0"

	currentCRD := buildCRD(crdName, nil, storedVersion("v2", true))
	currentCRD.Status.StoredVersions = []string{"v2"}

	newCRD := buildCRD(crdName, nil,
		storedVersion("v2", false),
		storedVersion("v3", true),
	)

	b, crdFake := buildBuilder(t, currentCRD, newCRD, csvName, csvManifestWithConversionWebhook(t, csvName, crdName))
	step := stepFor(crdName, csvName)

	s, err := b.create(step)
	require.NoError(t, err)
	_, err = s.Status()
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, "v3", updated.Annotations[install.PendingStorageVersionAnnotation],
		"annotation should record the deferred storage version")
	for _, v := range updated.Spec.Versions {
		if v.Name == "v2" {
			assert.True(t, v.Storage, "v2 should remain storage=true while promotion is deferred")
		} else {
			assert.False(t, v.Storage, "v3 should remain storage=false while promotion is deferred")
		}
	}
}

func TestNewCRDV1Step_SkipsDeferenceWhenBundleHasWebhookConversion(t *testing.T) {
	const crdName = "tests.example.com"
	const csvName = "operator.v1.0.0"

	currentCRD := buildCRD(crdName, nil, storedVersion("v2", true))
	currentCRD.Status.StoredVersions = []string{"v2"}

	newCRD := buildCRD(crdName, nil,
		storedVersion("v2", false),
		storedVersion("v3", true),
	)
	path := "/"
	port := int32(443)
	newCRD.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
		Strategy: apiextensionsv1.WebhookConverter,
		Webhook: &apiextensionsv1.WebhookConversion{
			ClientConfig: &apiextensionsv1.WebhookClientConfig{
				Service: &apiextensionsv1.ServiceReference{Path: &path, Port: &port},
			},
		},
	}

	b, crdFake := buildBuilder(t, currentCRD, newCRD, csvName, csvManifestWithConversionWebhook(t, csvName, crdName))
	step := stepFor(crdName, csvName)

	s, err := b.create(step)
	require.NoError(t, err)
	_, err = s.Status()
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.NotContains(t, updated.Annotations, install.PendingStorageVersionAnnotation,
		"annotation should not be set when bundle already has webhook conversion")
	var storageVersions []string
	for _, v := range updated.Spec.Versions {
		if v.Storage {
			storageVersions = append(storageVersions, v.Name)
		}
	}
	assert.Equal(t, []string{"v3"}, storageVersions, "v3 should be storage=true as written by the bundle")
}

func TestNewCRDV1Step_SkipsDeferenceWhenStorageVersionUnchanged(t *testing.T) {
	const crdName = "tests.example.com"
	const csvName = "operator.v1.0.0"

	currentCRD := buildCRD(crdName, nil, storedVersion("v2", true))
	currentCRD.Status.StoredVersions = []string{"v2"}
	newCRD := buildCRD(crdName, nil, storedVersion("v2", true))

	b, crdFake := buildBuilder(t, currentCRD, newCRD, csvName, csvManifestWithConversionWebhook(t, csvName, crdName))
	step := stepFor(crdName, csvName)

	s, err := b.create(step)
	require.NoError(t, err)
	_, err = s.Status()
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.NotContains(t, updated.Annotations, install.PendingStorageVersionAnnotation,
		"annotation should not be set when storage version does not change")
}

// TestNewCRDV1Step_SkipsDeferenceWhenCurrentVersionDroppedFromNewSpec verifies that
// the deferral block does not revert storage markers when the current storage version
// is absent from the new bundle's spec. Reverting in that case would produce a CRD
// with no Storage=true version, which the API server rejects with 422.
func TestNewCRDV1Step_SkipsDeferenceWhenCurrentVersionDroppedFromNewSpec(t *testing.T) {
	const crdName = "tests.example.com"
	const csvName = "operator.v1.0.0"

	// Current cluster CRD: only v2 as storage; Status.StoredVersions is empty so
	// SafeStorageVersionUpgrade won't block the removal of v2 from the new spec.
	currentCRD := buildCRD(crdName, nil, storedVersion("v2", true))
	// No Status.StoredVersions — SafeStorageVersionUpgrade passes even though v2 is dropped.

	// New bundle: drops v2 entirely, promotes v3 as the sole version.
	newCRD := buildCRD(crdName, nil, storedVersion("v3", true))

	b, crdFake := buildBuilder(t, currentCRD, newCRD, csvName, csvManifestWithConversionWebhook(t, csvName, crdName))
	step := stepFor(crdName, csvName)

	s, err := b.create(step)
	require.NoError(t, err)
	_, err = s.Status()
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.NotContains(t, updated.Annotations, install.PendingStorageVersionAnnotation,
		"annotation should not be set when currentVer is absent from the new bundle spec — reverting would produce zero storage versions")
	var storageVersions []string
	for _, v := range updated.Spec.Versions {
		if v.Storage {
			storageVersions = append(storageVersions, v.Name)
		}
	}
	assert.Equal(t, []string{"v3"}, storageVersions, "v3 should be written as storage=true as the bundle intended")
}

func TestNewCRDV1Step_SkipsDeferenceWhenCSVHasNoConversionWebhook(t *testing.T) {
	const crdName = "tests.example.com"
	const csvName = "operator.v1.0.0"

	currentCRD := buildCRD(crdName, nil, storedVersion("v2", true))
	currentCRD.Status.StoredVersions = []string{"v2"}
	newCRD := buildCRD(crdName, nil,
		storedVersion("v2", false),
		storedVersion("v3", true),
	)

	// CSV does NOT list a ConversionWebhook for this CRD
	csvNoWebhook := csvManifestWithConversionWebhook(t, csvName, "other.example.com")

	b, crdFake := buildBuilder(t, currentCRD, newCRD, csvName, csvNoWebhook)
	step := stepFor(crdName, csvName)

	s, err := b.create(step)
	require.NoError(t, err)
	_, err = s.Status()
	require.NoError(t, err)

	updated, err := crdFake.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.NotContains(t, updated.Annotations, install.PendingStorageVersionAnnotation,
		"annotation should not be set when CSV declares no conversion webhook for this CRD")
	var storageVersions []string
	for _, v := range updated.Spec.Versions {
		if v.Storage {
			storageVersions = append(storageVersions, v.Name)
		}
	}
	assert.Equal(t, []string{"v3"}, storageVersions, "v3 should be written as storage=true unchanged")
}
