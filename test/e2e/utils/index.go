package utils

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const defaultIndexName = "operator-index-registry"

func (r *Registry) CreateAndUploadIndex(indexName string, bundleReferences []string) (string, error) {
	if len(indexName) == 0 {
		indexName = defaultIndexName
	}
	bundleString := strings.Join(bundleReferences, ",")
	if len(bundleString) == 0 {
		bundleString = "\"\""
	}
	indexReference := fmt.Sprintf("%s/%s:latest", r.URL, indexName)
	BundleCreateCmd := exec.Command("opm", "index", "add", "--tag", indexReference, "--pull-tool", "docker", "-u", "docker", "--skip-tls", "--bundles", bundleString)
	BundleCreateCmd.Stdout = os.Stdout
	BundleCreateCmd.Stderr = os.Stderr
	if err := BundleCreateCmd.Run(); err != nil {
		return "", err
	}

	BundleUploadCommand := exec.Command("docker", "push", indexReference)
	BundleUploadCommand.Stdout = os.Stdout
	BundleUploadCommand.Stderr = os.Stderr
	if err := BundleUploadCommand.Run(); err != nil {
		return "", err
	}
	return indexReference, nil
}
