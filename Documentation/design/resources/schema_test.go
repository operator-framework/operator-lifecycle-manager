package resources

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"bufio"
	"fmt"
	"strings"

	"encoding/json"

	"github.com/ghodss/yaml"
	openapispec "github.com/go-openapi/spec"
	"github.com/go-openapi/validate"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	apiservervalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"path/filepath"

	"io"

	"github.com/go-openapi/strfmt"
)

func ReadPragmas(fileReader *bufio.Reader) (pragmas []string, err error) {
	for {
		maybePragma, err := fileReader.ReadString('\n')
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

func ValidateUsingPragma(t *testing.T, pragma string, fileReader io.Reader) error {
	const ValidateCRDPrefix = "validate-crd:"
	switch {
	case strings.HasPrefix(pragma, ValidateCRDPrefix):
		return ValidateCRD(t, strings.TrimSpace(strings.TrimPrefix(pragma, ValidateCRDPrefix)), fileReader)
	}
	return nil
}

func ValidateCRD(t *testing.T, schemaFileName string, fileReader io.Reader) error {
	schemaBytes, err := ioutil.ReadFile(schemaFileName)
	require.NoError(t, err)

	schemaBytesJson, err := yaml.YAMLToJSON(schemaBytes)
	require.NoError(t, err)
	var parsedSchema map[string]interface{}
	err = json.Unmarshal(schemaBytesJson, &parsedSchema)
	require.NoError(t, err)

	crd := v1beta1.CustomResourceDefinition{}
	json.Unmarshal(schemaBytesJson, &crd)

	exampleFileBytes, err := ioutil.ReadAll(fileReader)
	require.NoError(t, err)
	exampleFileBytesJson, err := yaml.YAMLToJSON(exampleFileBytes)
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
	pragmas, err := ReadPragmas(fileReader)
	require.NoError(t, err)
	for _, pragma := range pragmas {
		err := ValidateUsingPragma(t, pragma, fileReader)
		if err != nil {
			t.Errorf("validating %s: %v", path, err)
		}
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
