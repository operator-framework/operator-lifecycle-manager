package ctx

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	pversioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"
)

var ctx TestContext

// TestContext represents the environment of an executing test. It can
// be considered roughly analogous to a kubeconfig context.
type TestContext struct {
	restConfig     *rest.Config
	kubeClient     operatorclient.ClientInterface
	operatorClient versioned.Interface
	dynamicClient  dynamic.Interface
	packageClient  pversioned.Interface
}

// Ctx returns a pointer to the global test context. During parallel
// test executions, Ginkgo starts one process per test "node", and
// each node will have its own context, which may or may not point to
// the same test cluster.
func Ctx() *TestContext {
	return &ctx
}

func (ctx TestContext) Logf(f string, v ...interface{}) {
	if !strings.HasSuffix(f, "\n") {
		f += "\n"
	}
	fmt.Fprintf(GinkgoWriter, f, v...)
}

func (ctx TestContext) RESTConfig() *rest.Config {
	return ctx.restConfig
}

func (ctx TestContext) KubeClient() operatorclient.ClientInterface {
	return ctx.kubeClient
}

func (ctx TestContext) OperatorClient() versioned.Interface {
	return ctx.operatorClient
}

func (ctx TestContext) DynamicClient() dynamic.Interface {
	return ctx.dynamicClient
}

func (ctx TestContext) PackageClient() pversioned.Interface {
	return ctx.packageClient
}

func setDerivedFields(ctx *TestContext) error {
	if ctx == nil {
		return fmt.Errorf("nil test context")
	}

	if ctx.restConfig == nil {
		return fmt.Errorf("nil RESTClient")
	}

	kubeClient, err := operatorclient.NewClientFromRestConfig(ctx.restConfig)
	if err != nil {
		return err
	}
	ctx.kubeClient = kubeClient

	operatorClient, err := versioned.NewForConfig(ctx.restConfig)
	if err != nil {
		return err
	}
	ctx.operatorClient = operatorClient

	dynamicClient, err := dynamic.NewForConfig(ctx.restConfig)
	if err != nil {
		return err
	}
	ctx.dynamicClient = dynamicClient

	packageClient, err := pversioned.NewForConfig(ctx.restConfig)
	if err != nil {
		return err
	}
	ctx.packageClient = packageClient

	return nil
}
