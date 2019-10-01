package bundle

import (
	"io/ioutil"
	"os"
	"path/filepath"
)

const (
	operatorDir  = "/test-operator"
	manifestsDir = "/0.0.1"
	helmFile     = "Chart.yaml"
	csvFile      = "test.clusterserviceversion.yaml"
	crdFile      = "test.crd.yaml"
)

func setup(input string) {
	// Create test directory
	testDir := operatorDir + manifestsDir
	createDir(testDir)

	// Create test files in test directory
	createFiles(testDir, input)
}

func cleanup() {
	// Remove test directory
	os.RemoveAll(operatorDir)
}

func createDir(dir string) {
	os.MkdirAll(dir, os.ModePerm)
}

func createFiles(dir, input string) {
	// Create test files in test directory
	switch input {
	case registryV1Type:
		file, _ := os.Create(filepath.Join(dir, csvFile))
		file.Close()
	case helmType:
		file, _ := os.Create(filepath.Join(dir, helmFile))
		file.Close()
	case plainType:
		file, _ := os.Create(filepath.Join(dir, crdFile))
		file.Close()
	default:
		break
	}
}

func clearDir(dir string) {
	items, _ := ioutil.ReadDir(dir)

	for _, item := range items {
		if item.IsDir() {
			continue
		} else {
			os.Remove(item.Name())
		}
	}
}
