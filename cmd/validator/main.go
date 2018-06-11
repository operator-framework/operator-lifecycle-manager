package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/operator-framework/operator-lifecycle-manager/cmd/validator/schema"
)

func main() {
	manifestDir := os.Args[1]

	schema.TestCatalogResources(manifestDir)

	filepath.Walk(manifestDir, func(path string, f os.FileInfo, err error) error {
		if path == manifestDir || !f.IsDir() {
			return nil
		}

		fmt.Printf("Validating upgrade path for %s in %s\n", f.Name(), path)
		schema.TestUpgradePath(path)
		return nil
	})
}
