package main

import (
	schema "github.com/operator-framework/operator-lifecycle-manager/cmd/validator/schema"
)

func main() {
	// TODO(alecmerdler): Validate stuff
	schema.TestCatalogVersions()
	schema.TestCatalogResources()
}
