package resources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
	openapispec "github.com/go-openapi/spec"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	apiservervalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	apiValidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func ReadPragmas(fileBytes []byte) (pragmas []string, err error) {
	fileReader := bytes.NewReader(fileBytes)
	fileBufReader := bufio.NewReader(fileReader)
	for {
		maybePragma, err := fileBufReader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(maybePragma, "#!") {
			pragmas = append(pragmas, strings.TrimSpace(strings.TrimPrefix(maybePragma, "#!")))
		} else {
			// pragmas must be defined at the top of the file, stop when we don't see a line with the pragma mark
			break
		}
	}
	return
}

type Meta struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
}

func (m *Meta) GetObjectKind() schema.ObjectKind {
	return m
}
func (in *Meta) DeepCopyInto(out *Meta) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	return
}

func (in *Meta) DeepCopy() *Meta {
	if in == nil {
		return nil
	}
	out := new(Meta)
	in.DeepCopyInto(out)
	return out
}

func (in *Meta) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	} else {
		return nil
	}
}

func ValidateKubectlable(t *testing.T, fileBytes []byte) error {
	exampleFileBytesJson, err := yaml.YAMLToJSON(fileBytes)
	if err != nil {
		return err
	}
	parsedMeta := &Meta{}
	err = json.Unmarshal(exampleFileBytesJson, parsedMeta)
	if err != nil {
		return err
	}
	requiresNamespace := parsedMeta.Kind != "CustomResourceDefinition"
	errs := apiValidation.ValidateObjectMeta(
		&parsedMeta.ObjectMeta,
		requiresNamespace,
		func(s string, prefix bool) []string {
			return nil
		},
		field.NewPath("metadata"),
	)

	if len(errs) > 0 {
		return fmt.Errorf("error validating object metadata: %s. %v. %s", errs, parsedMeta, string(exampleFileBytesJson))
	}
	return nil
}

func ValidateUsingPragma(t *testing.T, pragma string, fileBytes []byte) error {
	const ValidateCRDPrefix = "validate-crd:"
	switch {
	case strings.HasPrefix(pragma, ValidateCRDPrefix):
		return ValidateCRD(t, strings.TrimSpace(strings.TrimPrefix(pragma, ValidateCRDPrefix)), fileBytes)
	}
	return nil
}

func ValidateCRD(t *testing.T, schemaFileName string, fileBytes []byte) error {
	schemaBytes, err := ioutil.ReadFile(schemaFileName)
	require.NoError(t, err)

	schemaBytesJson, err := yaml.YAMLToJSON(schemaBytes)
	require.NoError(t, err)
	var parsedSchema map[string]interface{}
	err = json.Unmarshal(schemaBytesJson, &parsedSchema)
	require.NoError(t, err)

	crd := v1beta1.CustomResourceDefinition{}
	json.Unmarshal(schemaBytesJson, &crd)

	exampleFileBytesJson, err := yaml.YAMLToJSON(fileBytes)
	unstructured := unstructured.Unstructured{}
	err = json.Unmarshal(exampleFileBytesJson, &unstructured)
	require.NoError(t, err)

	// Validate CRD definition statically

	// enable alpha feature CustomResourceValidation
	err = utilfeature.DefaultFeatureGate.Set("CustomResourceValidation=true")
	require.NoError(t, err)

	scheme := runtime.NewScheme()
	err = apiextensions.AddToScheme(scheme)
	require.NoError(t, err)
	err = v1beta1.AddToScheme(scheme)
	require.NoError(t, err)
	convertedCRD := apiextensions.CustomResourceDefinition{}
	scheme.Convert(&crd, &convertedCRD, nil)

	errList := validation.ValidateCustomResourceDefinition(&convertedCRD)
	if len(errList) > 0 {
		for _, ferr := range errList {
			fmt.Println(ferr)
		}
		t.Errorf("CRD failed validation: %s", schemaFileName)
	}

	// Validate CR against CRD schema
	openapiSchema := &openapispec.Schema{}
	err = apiservervalidation.ConvertToOpenAPITypes(&convertedCRD, openapiSchema)
	require.NoError(t, err)
	err = openapispec.ExpandSchema(openapiSchema, nil, nil)
	require.NoError(t, err)
	validator := validate.NewSchemaValidator(openapiSchema, nil, "", strfmt.Default)
	return apiservervalidation.ValidateCustomResource(unstructured.UnstructuredContent()["spec"], validator)
}

func ValidateResource(t *testing.T, path string, f os.FileInfo, err error) error {
	require.NoError(t, err)

	exampleFileReader, err := os.Open(path)
	require.NoError(t, err)
	defer exampleFileReader.Close()

	fileReader := bufio.NewReader(exampleFileReader)
	fileBytes, err := ioutil.ReadAll(fileReader)
	require.NoError(t, err)
	pragmas, err := ReadPragmas(fileBytes)
	require.NoError(t, err)
	for _, pragma := range pragmas {
		fileReader.Reset(exampleFileReader)
		err := ValidateUsingPragma(t, pragma, fileBytes)
		if err != nil {
			t.Errorf("validating %s: %v", path, err)
		}
	}
	err = ValidateKubectlable(t, fileBytes)
	if err != nil {
		t.Errorf("validating %s: %v", path, err)
	}
	return nil
}

type DirectoryResourceValidator struct {
	t *testing.T
}

func (d *DirectoryResourceValidator) ValidateResources(directory string) {
	err := filepath.Walk(".", d.ValidateResource)
	require.NoError(d.t, err)
}

func (d *DirectoryResourceValidator) ValidateResource(path string, f os.FileInfo, err error) error {
	if f.IsDir() {
		return nil
	}

	if !strings.HasSuffix(path, ".yaml") {
		return nil
	}

	d.t.Run(fmt.Sprintf("validate %s", path), func(t *testing.T) {
		require.NoError(t, ValidateResource(t, path, f, err))
	})
	return nil
}

func TestResourceExamples(t *testing.T) {
	directoryTester := DirectoryResourceValidator{t}
	directoryTester.ValidateResources(".")
}
