package bundle

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	DefaultPermission   = 0644
	RegistryV1Type      = "registry+v1"
	PlainType           = "plain"
	HelmType            = "helm"
	AnnotationsFile     = "annotations.yaml"
	DockerFile          = "bundle.Dockerfile"
	ManifestsDir        = "manifests/"
	MetadataDir         = "metadata/"
	ManifestsLabel      = "operators.operatorframework.io.bundle.manifests.v1"
	MetadataLabel       = "operators.operatorframework.io.bundle.metadata.v1"
	MediatypeLabel      = "operators.operatorframework.io.bundle.mediatype.v1"
	PackageLabel        = "operators.operatorframework.io.bundle.package.v1"
	ChannelsLabel       = "operators.operatorframework.io.bundle.channels.v1"
	ChannelDefaultLabel = "operators.operatorframework.io.bundle.channel.default.v1"
)

type AnnotationMetadata struct {
	Annotations map[string]string `yaml:"annotations" json:"annotations"`
}

// GenerateFunc builds annotations.yaml with mediatype, manifests &
// metadata directories in bundle image, package name, channels and default
// channels information and then writes the file to `/metadata` directory.
// Inputs:
// @directory: The local directory where bundle manifests and metadata are located
// @outputDir: Optional generated path where the /manifests and /metadata directories are copied
// as they would appear on the bundle image
// @packageName: The name of the package that bundle image belongs to
// @channels: The list of channels that bundle image belongs to
// @channelDefault: The default channel for the bundle image
// @overwrite: Boolean flag to enable overwriting annotations.yaml locally if existed
func GenerateFunc(directory, outputDir, packageName, channels, channelDefault string, overwrite bool) error {
	// clean the input so that we know the absolute paths of input directories
	directory, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if outputDir != "" {
		outputDir, err = filepath.Abs(outputDir)
		if err != nil {
			return err
		}
	}

	_, err = os.Stat(directory)
	if os.IsNotExist(err) {
		return err
	}

	// Determine mediaType
	mediaType, err := GetMediaType(directory)
	if err != nil {
		return err
	}

	// Get directory context for file output
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Channels and packageName are required fields where as default channel is automatically filled if unspecified
	// and that either of the required field is missing. We are interpreting the bundle information through
	// bundle directory embedded in the package folder.
	if channels == "" || packageName == "" {
		var notProvided []string
		if channels == "" {
			notProvided = append(notProvided, "channels")
		}
		if packageName == "" {
			notProvided = append(notProvided, "package name")
		}
		log.Infof("Bundle %s information not provided, inferring from parent package directory",
			strings.Join(notProvided, " and "))

		i, err := NewBundleDirInterperter(directory)
		if err != nil {
			return fmt.Errorf("please manually input channels and packageName, "+
				"error interpreting bundle from directory %s, %v", directory, err)
		}

		if channels == "" {
			channels = strings.Join(i.GetBundleChannels(), ",")
			if channels == "" {
				return fmt.Errorf("error interpreting channels, please manually input channels instead")
			}
			log.Infof("Inferred channels: %s", channels)
		}

		if packageName == "" {
			packageName = i.GetPackageName()
			log.Infof("Inferred package name: %s", packageName)
		}

		if channelDefault == "" {
			channelDefault = i.GetDefaultChannel()
			if !containsString(strings.Split(channels, ","), channelDefault) {
				channelDefault = ""
			}
			log.Infof("Inferred default channel: %s", channelDefault)
		}
	}

	log.Info("Building annotations.yaml")

	// Generate annotations.yaml
	content, err := GenerateAnnotations(mediaType, ManifestsDir, MetadataDir, packageName, channels, channelDefault)
	if err != nil {
		return err
	}

	// Push the output yaml content to the correct directory and conditionally copy the manifest dir
	outManifestDir, outMetadataDir, err := CopyYamlOutput(content, directory, outputDir, workingDir, overwrite)
	if err != nil {
		return err
	}

	log.Info("Building Dockerfile")

	// Generate Dockerfile
	content, err = GenerateDockerfile(mediaType, ManifestsDir, MetadataDir, outManifestDir, outMetadataDir, workingDir, packageName, channels, channelDefault)
	if err != nil {
		return err
	}

	_, err = os.Stat(filepath.Join(workingDir, DockerFile))
	if os.IsNotExist(err) || overwrite {
		err = WriteFile(DockerFile, workingDir, content)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		log.Infof("A bundle.Dockerfile already exists in current working directory: %s", workingDir)
	}

	return nil
}

// CopyYamlOutput takes the generated annotations yaml and writes it to disk.
// If an outputDir is specified, it will copy the input manifests
// It returns two strings. resultMetadata is the path to the output metadata/ folder.
// resultManifests is the path to the output manifests/ folder -- if no copy occured,
// it just returns the input manifestDir
func CopyYamlOutput(annotationsContent []byte, manifestDir, outputDir, workingDir string, overwrite bool) (resultManifests, resultMetadata string, err error) {
	// First, determine the parent directory of the metadata and manifest directories
	copyDir := ""

	// If an output directory is not defined defined, generate metadata folder into the same parent dir as existing manifest dir
	if outputDir == "" {
		copyDir = filepath.Dir(manifestDir)
		resultManifests = manifestDir
	} else { // otherwise copy the manifests into $outputDir/manifests and create the annotations file in $outputDir/metadata
		copyDir = outputDir

		log.Info("Generating output manifests directory")

		resultManifests = filepath.Join(copyDir, "/manifests/")
		// copy the manifest directory into $pwd/manifests/
		err := copyManifestDir(manifestDir, resultManifests, overwrite)
		if err != nil {
			return "", "", err
		}
	}

	// Now, generate the `metadata/` dir and write the annotations
	file, err := ioutil.ReadFile(filepath.Join(copyDir, MetadataDir, AnnotationsFile))
	if os.IsNotExist(err) || overwrite {
		writeDir := filepath.Join(copyDir, MetadataDir)
		err = WriteFile(AnnotationsFile, writeDir, annotationsContent)
		if err != nil {
			return "", "", err
		}
	} else if err != nil {
		return "", "", err
	} else {
		log.Infof("An annotations.yaml already exists in the directory: %s", MetadataDir)
		if err = ValidateAnnotations(file, annotationsContent); err != nil {
			return "", "", err
		}
	}

	resultMetadata = filepath.Join(copyDir, "metadata")

	return resultManifests, resultMetadata, nil
}

// GetMediaType determines mediatype from files (yaml) in given directory
// Currently able to detect helm chart, registry+v1 (CSV) and plain k8s resources
// such as CRD.
func GetMediaType(directory string) (string, error) {
	var files []string
	k8sFiles := make(map[string]*unstructured.Unstructured)

	// Read all file names in directory
	items, _ := ioutil.ReadDir(directory)
	for _, item := range items {
		if item.IsDir() {
			continue
		}

		files = append(files, item.Name())

		fileWithPath := filepath.Join(directory, item.Name())
		fileBlob, err := ioutil.ReadFile(fileWithPath)
		if err != nil {
			return "", fmt.Errorf("Unable to read file %s in bundle", fileWithPath)
		}

		dec := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(string(fileBlob)), 10)
		unst := &unstructured.Unstructured{}
		if err := dec.Decode(unst); err == nil {
			k8sFiles[item.Name()] = unst
		}
	}

	if len(files) == 0 {
		return "", fmt.Errorf("The directory %s contains no yaml files", directory)
	}

	// Validate if bundle is helm chart type
	if _, err := IsChartDir(directory); err == nil {
		return HelmType, nil
	}

	// Validate the files to determine media type
	for _, fileName := range files {
		// Check if one of the k8s files is a CSV
		if k8sFile, ok := k8sFiles[fileName]; ok {
			if k8sFile.GetObjectKind().GroupVersionKind().Kind == "ClusterServiceVersion" {
				return RegistryV1Type, nil
			}
		}
	}

	return PlainType, nil
}

// ValidateAnnotations validates existing annotations.yaml against generated
// annotations.yaml to ensure existing annotations.yaml contains expected values.
func ValidateAnnotations(existing, expected []byte) error {
	var fileAnnotations AnnotationMetadata
	var expectedAnnotations AnnotationMetadata

	log.Info("Validating existing annotations.yaml")

	err := yaml.Unmarshal(existing, &fileAnnotations)
	if err != nil {
		log.Errorf("Unable to parse existing annotations.yaml")
		return err
	}

	err = yaml.Unmarshal(expected, &expectedAnnotations)
	if err != nil {
		log.Errorf("Unable to parse expected annotations.yaml")
		return err
	}

	// Ensure each expected annotation key and value exist in existing.
	var errs []error
	for label, item := range expectedAnnotations.Annotations {
		value, hasAnnotation := fileAnnotations.Annotations[label]
		if !hasAnnotation {
			errs = append(errs, fmt.Errorf("Missing field: %s", label))
			continue
		}

		if item != value {
			errs = append(errs, fmt.Errorf("Expect field %q to have value %q instead of %q",
				label, item, value))
		}
	}

	return utilerrors.NewAggregate(errs)
}

// GenerateAnnotations builds annotations.yaml with mediatype, manifests &
// metadata directories in bundle image, package name, channels and default
// channels information.
func GenerateAnnotations(mediaType, manifests, metadata, packageName, channels, channelDefault string) ([]byte, error) {
	annotations := &AnnotationMetadata{
		Annotations: map[string]string{
			MediatypeLabel:      mediaType,
			ManifestsLabel:      manifests,
			MetadataLabel:       metadata,
			PackageLabel:        packageName,
			ChannelsLabel:       channels,
			ChannelDefaultLabel: channelDefault,
		},
	}

	annotations.Annotations[ChannelDefaultLabel] = channelDefault

	afile, err := yaml.Marshal(annotations)
	if err != nil {
		return nil, err
	}

	return afile, nil
}

// GenerateDockerfile builds Dockerfile with mediatype, manifests &
// metadata directories in bundle image, package name, channels and default
// channels information in LABEL section.
func GenerateDockerfile(mediaType, manifests, metadata, copyManifestDir, copyMetadataDir, workingDir, packageName, channels, channelDefault string) ([]byte, error) {
	var fileContent string

	relativeManifestDirectory, err := filepath.Rel(workingDir, copyManifestDir)
	if err != nil {
		return nil, err
	}

	relativeMetadataDirectory, err := filepath.Rel(workingDir, copyMetadataDir)
	if err != nil {
		return nil, err
	}

	// FROM
	fileContent += "FROM scratch\n\n"

	// LABEL
	fileContent += fmt.Sprintf("LABEL %s=%s\n", MediatypeLabel, mediaType)
	fileContent += fmt.Sprintf("LABEL %s=%s\n", ManifestsLabel, manifests)
	fileContent += fmt.Sprintf("LABEL %s=%s\n", MetadataLabel, metadata)
	fileContent += fmt.Sprintf("LABEL %s=%s\n", PackageLabel, packageName)
	fileContent += fmt.Sprintf("LABEL %s=%s\n", ChannelsLabel, channels)
	fileContent += fmt.Sprintf("LABEL %s=%s\n\n", ChannelDefaultLabel, channelDefault)

	// CONTENT
	fileContent += fmt.Sprintf("COPY %s %s\n", relativeManifestDirectory, "/manifests/")
	fileContent += fmt.Sprintf("COPY %s %s\n", relativeMetadataDirectory, "/metadata/")

	return []byte(fileContent), nil
}

// Write `fileName` file with `content` into a `directory`
// Note: Will overwrite the existing `fileName` file if it exists
func WriteFile(fileName, directory string, content []byte) error {
	if _, err := os.Stat(directory); os.IsNotExist(err) {
		err := os.MkdirAll(directory, os.ModePerm)
		if err != nil {
			return err
		}
	}
	log.Infof("Writing %s in %s", fileName, directory)
	err := ioutil.WriteFile(filepath.Join(directory, fileName), content, DefaultPermission)
	if err != nil {
		return err
	}
	return nil
}

// copy the contents of a potentially nested manifest dir into an output dir.
func copyManifestDir(from, to string, overwrite bool) error {
	fromFiles, err := ioutil.ReadDir(from)
	if err != nil {
		return err
	}

	if _, err := os.Stat(to); os.IsNotExist(err) {
		if err = os.MkdirAll(to, os.ModePerm); err != nil {
			return err
		}
	}

	for _, fromFile := range fromFiles {
		if fromFile.IsDir() {
			nestedTo := filepath.Join(to, filepath.Base(from))
			nestedFrom := filepath.Join(from, fromFile.Name())
			err = copyManifestDir(nestedFrom, nestedTo, overwrite)
			if err != nil {
				return err
			}
			continue
		}

		contents, err := os.Open(filepath.Join(from, fromFile.Name()))
		if err != nil {
			return err
		}
		defer func() {
			if err := contents.Close(); err != nil {
				log.Fatal(err)
			}
		}()

		toFilePath := filepath.Join(to, fromFile.Name())
		_, err = os.Stat(toFilePath)
		if err == nil && !overwrite {
			continue
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}

		toFile, err := os.Create(toFilePath)
		if err != nil {
			return err
		}
		defer func() {
			if err := toFile.Close(); err != nil {
				log.Fatal(err)
			}
		}()

		_, err = io.Copy(toFile, contents)
		if err != nil {
			return err
		}

		err = os.Chmod(toFilePath, fromFile.Mode())
		if err != nil {
			return err
		}
	}

	return nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
