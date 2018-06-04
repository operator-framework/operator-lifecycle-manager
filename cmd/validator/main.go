package main

import (
	"os"

	"github.com/operator-framework/operator-lifecycle-manager/cmd/validator/schema"
)

func main() {
	// TODO(alecmerdler): Get manifest directory from args
	manifestDir := os.Args[1]

	// FIXME(alecmerdler): `TestCatalogVersions` is meant to run against built catalog configmaps
	// schema.TestCatalogVersions(manifestDir)

	schema.TestCatalogResources(manifestDir)
}
