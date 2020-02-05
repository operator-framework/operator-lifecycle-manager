// +build !bare

package e2e

import (
	"flag"
	"os"
	"testing"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
)

var (
	kubeConfigPath = flag.String(
		"kubeconfig", "", "path to the kubeconfig file")

	namespace = flag.String(
		"namespace", "", "namespace where tests will run")

	olmNamespace = flag.String(
		"olmNamespace", "", "namespace where olm is running")

	communityOperators = flag.String(
		"communityOperators",
		"quay.io/operator-framework/upstream-community-operators@sha256:098457dc5e0b6ca9599bd0e7a67809f8eca397907ca4d93597380511db478fec",
		"reference to upstream-community-operators image")

	dummyImage = flag.String(
		"dummyImage",
		"bitnami/nginx:latest",
		"dummy image to treat as an operator in tests")

	testNamespace           = ""
	operatorNamespace       = ""
	communityOperatorsImage = ""
)

func TestMain(m *testing.M) {
	flag.Parse()

	testNamespace = *namespace
	operatorNamespace = *olmNamespace
	communityOperatorsImage = *communityOperators

	cleaner = newNamespaceCleaner(testNamespace)
	c, err := client.NewClient(*kubeConfigPath)
	if err != nil {
		panic(err)
	}

	groups, err := c.OperatorsV1().OperatorGroups(testNamespace).List(metav1.ListOptions{})
	if err != nil {
		panic(err)
	}
	if len(groups.Items) == 0 {
		_, err = c.OperatorsV1().OperatorGroups(testNamespace).Create(&v1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "opgroup",
				Namespace: testNamespace,
			},
		})
		if err != nil {
			panic(err)
		}
	}

	// run tests
	os.Exit(m.Run())
}
