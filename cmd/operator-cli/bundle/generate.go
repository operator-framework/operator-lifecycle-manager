package bundle

import (
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"gopkg.in/yaml.v2"
)

const (
	defaultPermission = 0644
	registryV1Type    = "registry+v1"
	plainType         = "plain"
	helmType          = "helm"
	manifestsMetadata = "manifests+metadata"
	annotationsFile   = "annotations.yaml"
	dockerFile        = "Dockerfile"
	resourcesLabel    = "operators.operatorframework.io.bundle.resources"
	mediatypeLabel    = "operators.operatorframework.io.bundle.mediatype"
)

type AnnotationMetadata struct {
	Annotations AnnotationType `yaml:"annotations"`
}

type AnnotationType struct {
	Resources string `yaml:"operators.operatorframework.io.bundle.resources"`
	MediaType string `yaml:"operators.operatorframework.io.bundle.mediatype"`
}

// newBundleBuildCmd returns a command that will build operator bundle image.
func newBundleGenerateCmd() *cobra.Command {
	bundleGenerateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate operator bundle metadata and Dockerfile",
		Long: `The operator-cli buindle generate command will generate operator
        bundle metadata if needed and a Dockerfile to build Operator bundle image.

        $ operator-cli bundle generate -d /test/0.0.1/
        `,
		RunE: generateFunc,
	}

	bundleGenerateCmd.Flags().StringVarP(&dirBuildArgs, "directory", "d", "", "The directory where bundle manifests are located.")
	if err := bundleGenerateCmd.MarkFlagRequired("directory"); err != nil {
		log.Fatalf("Failed to mark `directory` flag for `generate` subcommand as required")
	}

	return bundleGenerateCmd
}

func generateFunc(cmd *cobra.Command, args []string) error {
	var mediaType string

	// Determine mediaType
	mediaType, err := getMediaType(dirBuildArgs)
	if err != nil {
		return err
	}

	// Parent directory
	parentDir := path.Dir(path.Clean(dirBuildArgs))

	log.Info("Building annotations.yaml file")

	// Generate annotations.yaml
	content, err := generateAnnotationsFunc(manifestsMetadata, mediaType)
	if err != nil {
		return err
	}
	err = writeFile(annotationsFile, parentDir, content)
	if err != nil {
		return err
	}

	log.Info("Building Dockerfile")

	// Generate Dockerfile
	content = generateDockerfileFunc(manifestsMetadata, mediaType, dirBuildArgs)
	err = writeFile(dockerFile, parentDir, content)
	if err != nil {
		return err
	}

	return nil
}

func getMediaType(dirBuildArgs string) (string, error) {
	var files []string

	// Read all file names in directory
	items, _ := ioutil.ReadDir(dirBuildArgs)
	for _, item := range items {
		if item.IsDir() {
			continue
		} else {
			files = append(files, item.Name())
		}
	}

	if len(files) == 0 {
		return "", fmt.Errorf("The directory %s contains no files", dirBuildArgs)
	}

	// Validate the file names to determine media type
	for _, file := range files {
		if file == "Chart.yaml" {
			return helmType, nil
		} else if strings.HasSuffix(file, "clusterserviceversion.yaml") {
			return registryV1Type, nil
		} else {
			continue
		}
	}

	return plainType, nil
}

func generateAnnotationsFunc(resourcesType, mediaType string) ([]byte, error) {
	annotations := &AnnotationMetadata{
		Annotations: AnnotationType{
			Resources: resourcesType,
			MediaType: mediaType,
		},
	}

	afile, err := yaml.Marshal(annotations)
	if err != nil {
		return nil, err
	}

	return afile, nil
}

func generateDockerfileFunc(resourcesType, mediaType, directory string) []byte {
	var fileContent string

	metadataDir := path.Dir(path.Clean(directory))

	// FROM
	fileContent += "FROM scratch\n\n"

	// LABEL
	fileContent += fmt.Sprintf("LABEL %s=%s\n", resourcesLabel, resourcesType)
	fileContent += fmt.Sprintf("LABEL %s=%s\n\n", mediatypeLabel, mediaType)

	// CONTENT
	fileContent += fmt.Sprintf("ADD %s %s\n", directory, "/manifests")
	fileContent += fmt.Sprintf("ADD %s/%s %s%s\n", metadataDir, annotationsFile, "/metadata/", annotationsFile)

	return []byte(fileContent)
}

// Write `fileName` file with `content` into a `directory`
// Note: Will overwrite the existing `fileName` file if it exists
func writeFile(fileName, directory string, content []byte) error {
	err := ioutil.WriteFile(filepath.Join(directory, fileName), content, defaultPermission)
	if err != nil {
		return err
	}
	return nil
}
