package ctx

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	pversioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
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

	scheme *runtime.Scheme

	// client is the controller-runtime client -- we should use this from now on
	client controllerclient.Client
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

func (ctx TestContext) Scheme() *runtime.Scheme {
	return ctx.scheme
}

func (ctx TestContext) RESTConfig() *rest.Config {
	return rest.CopyConfig(ctx.restConfig)
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

func (ctx TestContext) Client() controllerclient.Client {
	return ctx.client
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

	ctx.scheme = runtime.NewScheme()
	localSchemeBuilder := runtime.NewSchemeBuilder(
		apiextensionsv1.AddToScheme,
		kscheme.AddToScheme,
		operatorsv1alpha1.AddToScheme,
		operatorsv1.AddToScheme,
		apiextensionsv1.AddToScheme,
	)
	if err := localSchemeBuilder.AddToScheme(ctx.scheme); err != nil {
		return err
	}

	client, err := controllerclient.New(ctx.restConfig, controllerclient.Options{
		Scheme: ctx.scheme,
	})
	if err != nil {
		return err
	}
	ctx.client = client

	return nil
}
