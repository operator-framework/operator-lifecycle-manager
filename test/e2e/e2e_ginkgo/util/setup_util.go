package util

import (
	"flag"
)

var (
	KubeConfigPath = flag.String(
		"kubeconfig", "", "path to the kubeconfig file")

	Namespace = flag.String(
		"namespace", "", "namespace where tests will run")

	OlmNamespace = flag.String(
		"olmNamespace", "", "namespace where olm is running")

	CommunityOperators = flag.String(
		"communityOperators",
		"quay.io/operator-framework/upstream-community-operators@sha256:098457dc5e0b6ca9599bd0e7a67809f8eca397907ca4d93597380511db478fec",
		"reference to upstream-community-operators image")

	DummyImage = flag.String(
		"dummyImage",
		"bitnami/nginx:latest",
		"dummy image to treat as an operator in tests")
)

// This function parses flags that are passed as command line arguments during the test run
func Setup() {
	flag.Parse()
}
