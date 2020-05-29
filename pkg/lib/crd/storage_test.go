package crd

import (
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

const crdName = "test"

func TestSafeStorageVersionUpgradeFailure(t *testing.T) {
	existingCRD := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha2",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
			},
		},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{
			StoredVersions: []string{"v1alpha2"},
		},
	}

	newCRD := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha3",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
			},
		},
	}

	safe, err := SafeStorageVersionUpgrade(existingCRD, newCRD)
	// expect safe to be false, since crd upgrade is not safe
	require.False(t, safe)
	// expect error, since crd upgrade is not safe
	require.Error(t, err, "expected error for unsafe CRD upgrade")
	// error should be related to the storage upgrade removing a version
	require.Contains(t, err.Error(), "new CRD removes version", "expected storage version error")
}

func TestSafeStorageVersionUpgradeSuccess(t *testing.T) {
	existingCRD := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha2",
					Served:  true,
					Storage: false,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
				{
					Name:    "v1alpha3",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
			},
		},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{
			StoredVersions: []string{"v1alpha2", "v1alpha3"},
		},
	}

	newCRD := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha2",
					Served:  true,
					Storage: false,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
				{
					Name:    "v1alpha3",
					Served:  true,
					Storage: false,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
				{
					Name:    "v1alpha4",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
			},
		},
	}

	safe, err := SafeStorageVersionUpgrade(existingCRD, newCRD)
	// expect safe to be true, since crd upgrade is safe
	require.True(t, safe)
	// expect no error, since crd upgrade is safe
	require.NoError(t, err, "did not expect error for safe CRD upgrade")
}