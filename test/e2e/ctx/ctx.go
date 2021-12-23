package ctx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	g "github.com/onsi/ginkgo"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv2 "github.com/operator-framework/api/pkg/operators/v2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	controllerclient "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/controller-runtime/client"
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
	ssaClient      *controllerclient.ServerSideApplier

	kubeconfigPath string
	artifactsDir   string

	scheme *runtime.Scheme

	// client is the controller-runtime client -- we should use this from now on
	client k8scontrollerclient.Client
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
	fmt.Fprintf(g.GinkgoWriter, f, v...)
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

func (ctx TestContext) Client() k8scontrollerclient.Client {
	return ctx.client
}

func (ctx TestContext) SSAClient() *controllerclient.ServerSideApplier {
	return ctx.ssaClient
}

func (ctx TestContext) DumpNamespaceArtifacts(namespace string) error {
	if ctx.artifactsDir == "" {
		ctx.Logf("$ARTIFACTS_DIR is unset -- not collecting failed test case logs")
		return nil
	}
	ctx.Logf("collecting logs in the %s artifacts directory", ctx.artifactsDir)

	logDir := filepath.Join(ctx.artifactsDir, namespace)
	if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
		return err
	}
	kubeconfigPath := ctx.kubeconfigPath
	if kubeconfigPath == "" {
		ctx.Logf("unable to determine kubeconfig path so defaulting to the $KUBECONFIG value")
		kubeconfigPath = os.Getenv("KUBECONFIG")
	}

	envvars := []string{
		"TEST_NAMESPACE=" + namespace,
		"TEST_ARTIFACTS_DIR=" + logDir,
		"KUBECONFIG=" + kubeconfigPath,
	}

	// compiled test binary running e2e tests is run from the root ./bin directory
	cmd := exec.Command("../test/e2e/collect-ci-artifacts.sh")
	cmd.Env = append(cmd.Env, envvars...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
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
		operatorsv2.AddToScheme,
		apiextensionsv1.AddToScheme,
	)
	if err := localSchemeBuilder.AddToScheme(ctx.scheme); err != nil {
		return err
	}

	client, err := k8scontrollerclient.New(ctx.restConfig, k8scontrollerclient.Options{
		Scheme: ctx.scheme,
	})
	if err != nil {
		return err
	}
	ctx.client = client

	ctx.ssaClient, err = controllerclient.NewForConfig(ctx.restConfig, ctx.scheme, "test.olm.registry")
	if err != nil {
		return err
	}

	return nil
}
