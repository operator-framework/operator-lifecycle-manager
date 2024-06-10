//go:build tools
// +build tools

// This file imports packages that support the code generation required for the operators and their tests
// These tools do not belong in bingo they need to be part of the main module because, either:
// - It includes resources that are not directly imported (or importable) in go (e.g. yamls files)
// - They share dependencies (e.g. k8s libraries) with the main module and should be kept in sync
package tools

// OPM
import _ "github.com/operator-framework/operator-registry/cmd/opm"

// The OLM API CRDs used as input to the code generators
import _ "github.com/operator-framework/api/crds" // operators.coreos.com CRD manifests

// mock-generation
// These tools are referenced in //go:generate directives in the code
import (
	_ "github.com/golang/mock/mockgen"
	_ "github.com/maxbrunsfeld/counterfeiter/v6"
)

// k8s code generators
// Kept in sync with the main module k8s libs
// Ensure underlying commands are executable
// Surface the k8s.io/code-generator/kube_codegen.sh script needed by scripts/update_codegen.sh
import (
	_ "k8s.io/code-generator"
	_ "k8s.io/kube-openapi/cmd/openapi-gen"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
