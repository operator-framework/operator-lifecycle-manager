package e2e_ginkgo

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	e2e "k8s.io/kubernetes/test/e2e/framework"

	g "github.com/onsi/ginkgo"
	o "github.com/onsi/gomega"
	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/e2e_ginkgo/util"
)

var (
	testNamespace     = ""
	operatorNamespace = ""
)

// This function bootstraps the test suite
func TestE2eGinkgo(t *testing.T) {
	o.RegisterFailHandler(g.Fail)
	g.RunSpecs(t, "E2eGinkgo Suite")
}

// This function initializes a client which is used to create an operator group for a given namespace
var _ = g.BeforeSuite(func() {

	util.Setup()

	testNamespace = *util.Namespace
	operatorNamespace = *util.OlmNamespace

	util.Cleaner = util.NewNamespaceCleaner(testNamespace)
	c, err := client.NewClient(*util.KubeConfigPath)
	if err != nil {
		e2e.Failf("Failed to create a kube client, error : %v", err)
	}

	groups, err := c.OperatorsV1().OperatorGroups(testNamespace).List(metav1.ListOptions{})
	if err != nil {
		e2e.Failf("Failed to list operator groups, error : %v", err)
	}
	if len(groups.Items) == 0 {
		_, err = c.OperatorsV1().OperatorGroups(testNamespace).Create(&v1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "opgroup",
				Namespace: testNamespace,
			},
		})
		if err != nil {
			e2e.Failf("Failed to create an operator group, error : %v", err)
		}
	}
})

// ToDO: Include clean up code after the tests are run
var _ = g.AfterSuite(func() {

})
