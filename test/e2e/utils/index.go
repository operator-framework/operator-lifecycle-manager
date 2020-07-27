package main

import (
	"fmt"
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
	indexReference := fmt.Sprintf("%s/%s:latest", r.url, indexName)
	r.logger.Debugf("Adding bundles %s to index %s", bundleString, indexReference)
	BundleCreateCmd := exec.Command("opm", "index", "add", "--tag", indexReference, "--pull-tool", "docker", "-u", "docker", "--skip-tls", "--bundles", bundleString)
	BundleCreateCmd.Stdout = r.logger.Out
	BundleCreateCmd.Stderr = r.logger.Out
	if err := BundleCreateCmd.Run(); err != nil {
		return "", err
	}

	r.logger.Debugf("Uploading bundle to registry")
	BundleUploadCommand := exec.Command("docker", "push", indexReference)
	BundleUploadCommand.Stdout = r.logger.Out
	BundleUploadCommand.Stderr = r.logger.Out
	if err := BundleUploadCommand.Run(); err != nil {
		return "", err
	}
	return indexReference, nil
}
