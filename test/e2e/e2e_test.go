package e2e

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

func init() {
	log.SetLogger(zap.New())
}

var (
	kubeConfigPath = flag.String(
		"kubeconfig", "", "path to the kubeconfig file")

	namespace = flag.String(
		"namespace", "", "namespace where tests will run")

	olmNamespace = flag.String(
		"olmNamespace", "", "namespace where olm is running")

	catalogNamespace = flag.String(
		"catalogNamespace", "", "namespace where the global catalog content is stored")

	communityOperators = flag.String(
		"communityOperators",
		"quay.io/operatorhubio/catalog:latest",
		"reference to upstream-community-operators image",
	)

	dummyImage = flag.String(
		"dummyImage",
		"bitnami/nginx:latest",
		"dummy image to treat as an operator in tests",
	)

	collectArtifactsScriptPath = flag.String(
		"gather-artifacts-script-path",
		"./collect-ci-artifacts.sh",
		"configures the relative/absolute path to the script resposible for collecting CI artifacts",
	)

	testdataPath = flag.String(
		"test-data-dir",
		"./testdata",
		"configures where to find the testdata directory",
	)

	testdataDir             = ""
	testNamespace           = ""
	operatorNamespace       = ""
	communityOperatorsImage = ""
	globalCatalogNamespace  = ""
	junitDir                = "junit"
)

func TestEndToEnd(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(1 * time.Minute)
	SetDefaultEventuallyPollingInterval(1 * time.Second)
	SetDefaultConsistentlyDuration(30 * time.Second)
	SetDefaultConsistentlyPollingInterval(1 * time.Second)

	RunSpecs(t, "End-to-end")
}

var deprovision func() = func() {}

// This function initializes a client which is used to create an operator group for a given namespace
var _ = BeforeSuite(func() {
	if kubeConfigPath != nil && *kubeConfigPath != "" {
		// This flag can be deprecated in favor of the kubeconfig provisioner:
		os.Setenv("KUBECONFIG", *kubeConfigPath)
	}
	if collectArtifactsScriptPath != nil && *collectArtifactsScriptPath != "" {
		os.Setenv("E2E_ARTIFACT_SCRIPT", *collectArtifactsScriptPath)
	}

	testNamespace = *namespace
	operatorNamespace = *olmNamespace
	communityOperatorsImage = *communityOperators
	globalCatalogNamespace = *catalogNamespace
	testdataDir = *testdataPath
	deprovision = ctx.MustProvision(ctx.Ctx())
	ctx.MustInstall(ctx.Ctx())

	var groups operatorsv1.OperatorGroupList
	Expect(ctx.Ctx().Client().List(context.Background(), &groups, client.InNamespace(testNamespace))).To(Succeed())
	if len(groups.Items) == 0 {
		og := operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "opgroup",
				Namespace: testNamespace,
			},
		}
		Expect(ctx.Ctx().Client().Create(context.TODO(), &og)).To(Succeed())
	}

	// Tests can assume the group in the test namespace has been reconciled at least once.
	Eventually(func() ([]operatorsv1.OperatorGroupStatus, error) {
		var groups operatorsv1.OperatorGroupList
		if err := ctx.Ctx().Client().List(context.Background(), &groups, client.InNamespace(testNamespace)); err != nil {
			return nil, err
		}
		var statuses []operatorsv1.OperatorGroupStatus
		for _, group := range groups.Items {
			statuses = append(statuses, group.Status)
		}
		return statuses, nil
	}).Should(And(
		HaveLen(1),
		ContainElement(Not(BeZero())),
	))

	// Why is this failing a lot?
	_, err := fetchCatalogSourceOnStatus(ctx.Ctx().OperatorClient(), "operatorhubio-catalog", operatorNamespace, catalogSourceRegistryPodSynced())
	if err != nil {
		name := "operatorhubio-catalog"
		fetched, err2 := ctx.Ctx().OperatorClient().OperatorsV1alpha1().CatalogSources(operatorNamespace).Get(context.Background(), name, metav1.GetOptions{})
		if err2 != nil || fetched == nil {
			fmt.Printf("failed to fetch catalogSource %s/%s: %v\n", operatorNamespace, name, err2)
		} else {
			fmt.Printf("catalogSource %s/%s:\n%v", operatorNamespace, name, fetched)
		}
	}
	Expect(err).NotTo(HaveOccurred())

})

var _ = AfterSuite(func() {
	if env := os.Getenv("SKIP_CLEANUP"); env != "" {
		fmt.Println("Skipping deprovisioning...")
		return
	}
	deprovision()
})
