package crd

import (
	"bytes"
	"reflect"
	"testing"

	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
)

func TestVersion(t *testing.T) {
	v1beta1CRD := apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "plums.cluster.com",
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       Kind,
			APIVersion: Group + "v1beta1",
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Versions: []apiextensionsv1beta1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1beta1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1beta1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
			},
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Plural:   "plums",
				Singular: "plum",
				Kind:     "plum",
				ListKind: "list" + "plum",
			},
			Scope: "Namespaced",
		},
	}

	scheme := runtime.NewScheme()
	err := apiextensionsv1beta1.AddToScheme(scheme)
	if err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer
	err = k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(&v1beta1CRD, &b)
	if err != nil {
		t.Fatal(err)
	}
	v1beta1Manifest := b.String()

	// get version
	result, err := GroupVersion(&v1beta1Manifest)
	if err != nil {
		t.Fatal(err)
	}

	target := schema.GroupVersion{Group: "apiextensions.k8s.io", Version: "v1beta1"}
	if !reflect.DeepEqual(result, target) {
		t.Fatalf("expected %#v, got %#v for v1beta1 CRD version", target, result)
	}
}
