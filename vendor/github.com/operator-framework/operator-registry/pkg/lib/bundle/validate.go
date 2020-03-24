package bundle

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	v1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	v "github.com/operator-framework/api/pkg/validation"
	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/registry"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiValidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	y "github.com/ghodss/yaml"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type Meta struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
}

// imageValidator is a struct implementation of the Indexer interface
type imageValidator struct {
	imageReader containertools.ImageReader
	logger      *log.Entry
}

// PullBundleImage shells out to a container tool and pulls a given image tag
// Then it unpacks the image layer filesystem contents and pushes the contents
// to a specified directory for further validation
func (i imageValidator) PullBundleImage(imageTag, directory string) error {
	i.logger.Debug("Pulling and unpacking container image")

	return i.imageReader.GetImageData(imageTag, directory)
}

// ValidateBundle takes a directory containing the contents of a bundle and validates
// the format of that bundle for correctness using these criteria
// 1. Validate if the directory has two required directories for /manifests and /metadata
// 2. Expecting bundle manifests files to be in /manifests and metadata files (including
// annotations.yaml) to be in /metadata
// 3. Validate the information in annotations to match the bundle contents such as
// its media type, and channel information.
// Inputs:
// directory: the directory which the /manifests and /metadata exist
// Outputs:
// error: ValidattionError which contains a list of errors
func (i imageValidator) ValidateBundleFormat(directory string) error {
	var manifestsFound, metadataFound bool
	var annotationsDir, manifestsDir string
	var validationErrors []error

	items, err := ioutil.ReadDir(directory)
	if err != nil {
		validationErrors = append(validationErrors, err)
	}

	for _, item := range items {
		if item.IsDir() {
			switch s := item.Name(); s {
			case strings.TrimSuffix(ManifestsDir, "/"):
				i.logger.Debug("Found manifests directory")
				manifestsFound = true
				manifestsDir = filepath.Join(directory, ManifestsDir)
			case strings.TrimSuffix(MetadataDir, "/"):
				i.logger.Debug("Found metadata directory")
				metadataFound = true
				annotationsDir = filepath.Join(directory, MetadataDir)
			}
		}
	}

	if manifestsFound == false {
		validationErrors = append(validationErrors, fmt.Errorf("Unable to locate manifests directory"))
	}
	if metadataFound == false {
		validationErrors = append(validationErrors, fmt.Errorf("Unable to locate metadata directory"))
	}

	// Break here if we can't even find the files
	if len(validationErrors) > 0 {
		return NewValidationError(validationErrors)
	}

	i.logger.Debug("Getting mediaType info from manifests directory")
	mediaType, err := GetMediaType(manifestsDir)
	if err != nil {
		validationErrors = append(validationErrors, err)
	}

	// Validate annotations.yaml
	annotationsFile, err := ioutil.ReadFile(filepath.Join(annotationsDir, AnnotationsFile))
	if err != nil {
		fmtErr := fmt.Errorf("Unable to read annotations.yaml file: %s", err.Error())
		validationErrors = append(validationErrors, fmtErr)
		return NewValidationError(validationErrors)
	}

	var fileAnnotations AnnotationMetadata

	annotations := map[string]string{
		MediatypeLabel:      mediaType,
		ManifestsLabel:      ManifestsDir,
		MetadataLabel:       MetadataDir,
		PackageLabel:        "",
		ChannelsLabel:       "",
		ChannelDefaultLabel: "",
	}

	i.logger.Debug("Validating annotations.yaml")

	err = yaml.Unmarshal(annotationsFile, &fileAnnotations)
	if err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("Unable to parse annotations.yaml file"))
	}

	for label, item := range annotations {
		val, ok := fileAnnotations.Annotations[label]
		if ok {
			i.logger.Debugf(`Found annotation "%s" with value "%s"`, label, val)
		} else {
			aErr := fmt.Errorf("Missing annotation %q", label)
			validationErrors = append(validationErrors, aErr)
		}

		switch label {
		case MediatypeLabel:
			if item != val {
				aErr := fmt.Errorf("Expecting annotation %q to have value %q instead of %q", label, item, val)
				validationErrors = append(validationErrors, aErr)
			}
		case ManifestsLabel:
			if item != ManifestsDir {
				aErr := fmt.Errorf("Expecting annotation %q to have value %q instead of %q", label, ManifestsDir, val)
				validationErrors = append(validationErrors, aErr)
			}
		case MetadataDir:
			if item != MetadataLabel {
				aErr := fmt.Errorf("Expecting annotation %q to have value %q instead of %q", label, MetadataDir, val)
				validationErrors = append(validationErrors, aErr)
			}
		case ChannelsLabel, ChannelDefaultLabel:
			if val == "" {
				aErr := fmt.Errorf("Expecting annotation %q to have non-empty value", label)
				validationErrors = append(validationErrors, aErr)
			} else {
				annotations[label] = val
			}
		}
	}

	_, err = ValidateChannelDefault(annotations[ChannelsLabel], annotations[ChannelDefaultLabel])
	if err != nil {
		validationErrors = append(validationErrors, err)
	}

	if len(validationErrors) > 0 {
		return NewValidationError(validationErrors)
	}

	return nil
}

// ValidateBundleContent confirms that the CSV and CRD files inside the bundle
// directory are valid and can be installed in a cluster. Other GVK types are
// also validated to confirm if they are "kubectl-able" to a cluster meaning
// if they can be applied to a cluster using `kubectl` provided users have all
// necessary permissions and configurations.
// Inputs:
// manifestDir: the directory which all bundle manifests files are located
// Outputs:
// error: ValidattionError which contains a list of errors
func (i imageValidator) ValidateBundleContent(manifestDir string) error {
	var validationErrors []error

	i.logger.Debug("Validating bundle contents")

	mediaType, err := GetMediaType(manifestDir)
	if err != nil {
		validationErrors = append(validationErrors, err)
	}

	switch mediaType {
	case HelmType:
		return nil
	}

	var csvName string
	unstObjs := []*unstructured.Unstructured{}
	csvValidator := v.ClusterServiceVersionValidator
	crdValidator := v.CustomResourceDefinitionValidator

	// Read all files in manifests directory
	items, err := ioutil.ReadDir(manifestDir)
	if err != nil {
		validationErrors = append(validationErrors, err)
	}

	for _, item := range items {
		fileWithPath := filepath.Join(manifestDir, item.Name())
		data, err := ioutil.ReadFile(fileWithPath)
		if err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("Unable to read file %s in supported types", fileWithPath))
			continue
		}

		dec := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 30)
		k8sFile := &unstructured.Unstructured{}
		err = dec.Decode(k8sFile)
		if err != nil {
			validationErrors = append(validationErrors, err)
			continue
		}

		unstObjs = append(unstObjs, k8sFile)
		gvk := k8sFile.GetObjectKind().GroupVersionKind()
		i.logger.Debugf(`Validating "%s" from file "%s"`, gvk.String(), item.Name())
		// Verify if the object kind is supported for RegistryV1 format
		ok, _ := IsSupported(gvk.Kind)
		if mediaType == RegistryV1Type && !ok {
			validationErrors = append(validationErrors, fmt.Errorf("%s is not supported type for registryV1 bundle: %s", gvk.Kind, fileWithPath))
			continue
		}

		if gvk.Kind == CSVKind {
			csv := &v1.ClusterServiceVersion{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(k8sFile.Object, csv)
			if err != nil {
				validationErrors = append(validationErrors, err)
				continue
			}

			csvName = csv.GetName()
			results := csvValidator.Validate(csv)
			if len(results) > 0 {
				for _, err := range results[0].Errors {
					validationErrors = append(validationErrors, err)
				}
			}
		} else if gvk.Kind == CRDKind {
			crd := &v1beta1.CustomResourceDefinition{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(k8sFile.Object, crd)
			if err != nil {
				validationErrors = append(validationErrors, err)
				continue
			}

			results := crdValidator.Validate(crd)
			if len(results) > 0 {
				for _, err := range results[0].Errors {
					validationErrors = append(validationErrors, err)
				}
			}
		} else {
			err := validateKubectlable(data)
			if err != nil {
				validationErrors = append(validationErrors, err)
			}
		}
	}

	// Validate the bundle object
	if len(unstObjs) > 0 {
		bundle := registry.NewBundle(csvName, "", "", unstObjs...)
		bundleValidator := v.BundleValidator
		results := bundleValidator.Validate(bundle)
		if len(results) > 0 {
			for _, err := range results[0].Errors {
				validationErrors = append(validationErrors, err)
			}
		}
	}

	if len(validationErrors) > 0 {
		return NewValidationError(validationErrors)
	}

	return nil
}

// Validate if the file is kubecle-able
func validateKubectlable(fileBytes []byte) error {
	exampleFileBytesJSON, err := y.YAMLToJSON(fileBytes)
	if err != nil {
		return err
	}

	parsedMeta := &Meta{}
	err = json.Unmarshal(exampleFileBytesJSON, parsedMeta)
	if err != nil {
		return err
	}

	errs := apiValidation.ValidateObjectMeta(
		&parsedMeta.ObjectMeta,
		false,
		func(s string, prefix bool) []string {
			return nil
		},
		field.NewPath("metadata"),
	)

	if len(errs) > 0 {
		return fmt.Errorf("error validating object metadata: %s. %v", errs.ToAggregate(), parsedMeta)
	}

	return nil
}
