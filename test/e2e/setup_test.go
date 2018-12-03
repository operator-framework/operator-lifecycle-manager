// +build !bare

package e2e

import (
	"flag"
	"os"
	"testing"
)

var (
	kubeConfigPath = flag.String(
		"kubeconfig", "", "path to the kubeconfig file")

	namespace = flag.String(
		"namespace", "", "namespace where tests will run")

	olmNamespace = flag.String(
		"olmNamespace", "", "namespace where olm is running")

	testNamespace     = ""
	operatorNamespace = ""
)

func TestMain(m *testing.M) {
	if err := flag.Set("logtostderr", "true"); err != nil {
		panic(err)
	}
	flag.Parse()

	testNamespace = *namespace
	operatorNamespace = *olmNamespace
	cleaner = newNamespaceCleaner(testNamespace)

	// run tests
	os.Exit(m.Run())
}
