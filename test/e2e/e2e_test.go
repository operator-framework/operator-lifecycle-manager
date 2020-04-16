package e2e

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
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

func TestEndToEnd(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(1 * time.Minute)
	SetDefaultEventuallyPollingInterval(1 * time.Second)
	RunSpecs(t, "End-to-end")
}

var deprovision func() = func() {}

// This function initializes a client which is used to create an operator group for a given namespace
var _ = BeforeSuite(func() {
	if kubeConfigPath != nil && *kubeConfigPath != "" {
		// This flag can be deprecated in favor of the kubeconfig provisioner:
		os.Setenv("KUBECONFIG", *kubeConfigPath)
	}

	testNamespace = *namespace
	operatorNamespace = *olmNamespace
	communityOperatorsImage = *communityOperators

	cleaner = newNamespaceCleaner(testNamespace)

	deprovision = ctx.MustProvision(ctx.Ctx())
	ctx.MustInstall(ctx.Ctx())

	groups, err := ctx.Ctx().OperatorClient().OperatorsV1().OperatorGroups(testNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err)
	}
	if len(groups.Items) == 0 {
		_, err = ctx.Ctx().OperatorClient().OperatorsV1().OperatorGroups(testNamespace).Create(context.TODO(), &v1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "opgroup",
				Namespace: testNamespace,
			},
		}, metav1.CreateOptions{})
		if err != nil {
			panic(err)
		}
	}
})

var _ = AfterSuite(func() {
	deprovision()
})
