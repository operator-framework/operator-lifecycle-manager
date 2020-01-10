package bundle

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"gopkg.in/yaml.v2"
	"helm.sh/helm/v3/pkg/chartutil"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	DefaultPermission   = 0644
	RegistryV1Type      = "registry+v1"
	PlainType           = "plain"
	HelmType            = "helm"
	AnnotationsFile     = "annotations.yaml"
	DockerFile          = "Dockerfile"
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
	Annotations map[string]string `yaml:"annotations"`
}

// GenerateFunc builds annotations.yaml with mediatype, manifests &
// metadata directories in bundle image, package name, channels and default
// channels information and then writes the file to `/metadata` directory.
// Inputs:
// @directory: The local directory where bundle manifests and metadata are located
// @packageName: The name of the package that bundle image belongs to
// @channels: The list of channels that bundle image belongs to
// @channelDefault: The default channel for the bundle image
// @overwrite: Boolean flag to enable overwriting annotations.yaml locally if existed
func GenerateFunc(directory, packageName, channels, channelDefault string, overwrite bool) error {
	_, err := os.Stat(directory)
	if os.IsNotExist(err) {
		return err
	}

	// Determine mediaType
	mediaType, err := GetMediaType(directory)
	if err != nil {
		return err
	}

	log.Info("Building annotations.yaml")

	// Generate annotations.yaml
	content, err := GenerateAnnotations(mediaType, ManifestsDir, MetadataDir, packageName, channels, channelDefault)
	if err != nil {
		return err
	}

	file, err := ioutil.ReadFile(filepath.Join(directory, MetadataDir, AnnotationsFile))
	if os.IsNotExist(err) || overwrite {
		err = WriteFile(AnnotationsFile, filepath.Join(directory, MetadataDir), content)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		log.Info("An annotations.yaml already exists in directory")
		if err = ValidateAnnotations(file, content); err != nil {
			return err
		}
	}

	log.Info("Building Dockerfile")

	// Generate Dockerfile
	content, err = GenerateDockerfile(mediaType, ManifestsDir, MetadataDir, packageName, channels, channelDefault)
	if err != nil {
		return err
	}

	err = WriteFile(DockerFile, directory, content)
	if err != nil {
		return err
	}

	return nil
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
	if _, err := chartutil.IsChartDir(directory); err == nil {
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

	if len(fileAnnotations.Annotations) != len(expectedAnnotations.Annotations) {
		return fmt.Errorf("Unmatched number of fields. Expected (%d) vs existing (%d)",
			len(expectedAnnotations.Annotations), len(fileAnnotations.Annotations))
	}

	for label, item := range expectedAnnotations.Annotations {
		value, ok := fileAnnotations.Annotations[label]
		if ok == false {
			return fmt.Errorf("Missing field: %s", label)
		}

		if item != value {
			return fmt.Errorf(`Expect field "%s" to have value "%s" instead of "%s"`,
				label, item, value)
		}
	}

	return nil
}

// ValidateChannelDefault validates provided default channel to ensure it exists in
// provided channel list.
func ValidateChannelDefault(channels, channelDefault string) (string, error) {
	var chanDefault string
	var chanErr error
	channelList := strings.Split(channels, ",")

	if channelDefault != "" {
		for _, channel := range channelList {
			if channel == channelDefault {
				chanDefault = channelDefault
				break
			}
		}
		if chanDefault == "" {
			chanDefault = channelList[0]
			chanErr = fmt.Errorf(`The channel list "%s" doesn't contain channelDefault "%s"`, channels, channelDefault)
		}
	} else {
		chanDefault = channelList[0]
	}

	if chanDefault != "" {
		return chanDefault, chanErr
	} else {
		return chanDefault, fmt.Errorf("Invalid channels is provied: %s", channels)
	}
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

	chanDefault, err := ValidateChannelDefault(channels, channelDefault)
	if err != nil {
		return nil, err
	}

	annotations.Annotations[ChannelDefaultLabel] = chanDefault

	afile, err := yaml.Marshal(annotations)
	if err != nil {
		return nil, err
	}

	return afile, nil
}

// GenerateDockerfile builds Dockerfile with mediatype, manifests &
// metadata directories in bundle image, package name, channels and default
// channels information in LABEL section.
func GenerateDockerfile(mediaType, manifests, metadata, packageName, channels, channelDefault string) ([]byte, error) {
	var fileContent string

	chanDefault, err := ValidateChannelDefault(channels, channelDefault)
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
	fileContent += fmt.Sprintf("LABEL %s=%s\n\n", ChannelDefaultLabel, chanDefault)

	// CONTENT
	fileContent += fmt.Sprintf("COPY %s %s\n", "/*.yaml", "/manifests/")
	fileContent += fmt.Sprintf("COPY %s %s%s\n", filepath.Join("/", metadata, AnnotationsFile), "/metadata/", AnnotationsFile)

	return []byte(fileContent), nil
}

// Write `fileName` file with `content` into a `directory`
// Note: Will overwrite the existing `fileName` file if it exists
func WriteFile(fileName, directory string, content []byte) error {
	if _, err := os.Stat(directory); os.IsNotExist(err) {
		os.Mkdir(directory, os.ModePerm)
	}

	err := ioutil.WriteFile(filepath.Join(directory, fileName), content, DefaultPermission)
	if err != nil {
		return err
	}
	return nil
}
