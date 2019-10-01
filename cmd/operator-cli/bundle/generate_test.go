package bundle

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

func TestGetMediaType(t *testing.T) {
	setup("")
	defer cleanup()

	testDir := operatorDir + manifestsMetadata
	tests := []struct {
		directory string
		mediaType string
		errorMsg  string
	}{
		{
			testDir,
			registryV1Type,
			"",
		},
		{
			testDir,
			helmType,
			"",
		},
		{
			testDir,
			plainType,
			"",
		},
		{
			testDir,
			"",
			fmt.Sprintf("The directory %s contains no files", testDir),
		},
	}

	for _, item := range tests {
		createFiles(testDir, item.mediaType)
		manifestType, err := getMediaType(item.directory)
		if item.errorMsg == "" {
			require.Equal(t, item.mediaType, manifestType)
		} else {
			require.Equal(t, item.errorMsg, err.Error())
		}
		clearDir(testDir)
	}
}

func TestGenerateAnnotationsFunc(t *testing.T) {
	// Create test annotations struct
	testAnnotations := &AnnotationMetadata{
		Annotations: AnnotationType{
			Resources: "test1",
			MediaType: "test2",
		},
	}
	// Create result annotations struct
	resultAnnotations := AnnotationMetadata{}
	data, err := generateAnnotationsFunc("test1", "test2")
	require.NoError(t, err)

	err = yaml.Unmarshal(data, &resultAnnotations)
	require.NoError(t, err)

	require.Equal(t, testAnnotations.Annotations.Resources, resultAnnotations.Annotations.Resources)
	require.Equal(t, testAnnotations.Annotations.MediaType, resultAnnotations.Annotations.Resources)
}

func TestGenerateDockerfileFunc(t *testing.T) {
	testDir := operatorDir + manifestsMetadata
	output := "FROM scratch\n\n" +
		"LABEL operators.operatorframework.io.bundle.resources=test1\n" +
		"LABEL operators.operatorframework.io.bundle.mediatype=test2\n\n" +
		"ADD test-operator/0.0.1 /manifests\n" +
		"ADD test-operator/0.0.1/annotations.yaml /metadata/annotations.yaml\n"

	content := generateDockerfileFunc("test1", "test2", testDir)
	require.Equal(t, output, string(content))
}
