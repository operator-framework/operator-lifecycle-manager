package openshift

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/operator-framework/api/crds"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// TestMain disables WatchListClient feature for all tests in this package.
// This is required because envtest doesn't fully support WatchList semantics in K8s 1.35.
// See: https://github.com/kubernetes/kubernetes/issues/135895
func TestMain(m *testing.M) {
	// Disable WatchListClient feature gate
	os.Setenv("KUBE_FEATURE_WatchListClient", "false")
	os.Exit(m.Run())
}

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "OpenShift Suite")
}

var (
	testEnv   *envtest.Environment
	k8sClient client.Client
	ctx       context.Context
	fixedNow  NowFunc

	syncCh chan error
)

const (
	clusterOperator     = "operator-lifecycle-manager"
	controllerNamespace = "default"
	timeout             = time.Second * 20
	clusterVersion      = "1.0.0+cluster"
	controllerVersion   = "1.0.0+controller"
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// Note: OpenShift CRDs are loaded from testdata instead of vendor.
	// Breaking change: The github.com/openshift/api package (v0.0.0-20251111193948+)
	// no longer ships individual CRD YAML files in vendor (they were removed in favor
	// of a consolidated manifest). We generate minimal CRDs via scripts/generate_openshift_crds.sh
	// and load them from testdata to keep tests self-contained and work in envtest/kind environments.
	testEnv = &envtest.Environment{
		CRDs: []*apiextensionsv1.CustomResourceDefinition{
			crds.ClusterServiceVersion(),
		},
		CRDDirectoryPaths: []string{
			filepath.Join("testdata", "crds"),
		},
	}

	cfg, err := testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	ctx = context.Background()
	now := metav1.Date(2021, time.April, 13, 0, 0, 0, 0, time.Local)
	fixedNow = func() metav1.Time {
		return now
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).ToNot(HaveOccurred())

	// Register OpenShift types with the scheme
	Expect(configv1.AddToScheme(mgr.GetScheme())).To(Succeed())

	k8sClient = mgr.GetClient()

	syncCh = make(chan error)
	reconciler, err := NewClusterOperatorReconciler(
		WithClient(k8sClient),
		WithScheme(mgr.GetScheme()),
		WithName(clusterOperator),
		WithNamespace(controllerNamespace),
		WithSyncChannel(syncCh),
		WithOLMOperator(),
		WithNow(fixedNow),
		WithTargetVersions(
			configv1.OperandVersion{
				Name:    "operator",
				Version: clusterVersion,
			},
			configv1.OperandVersion{
				Name:    clusterOperator,
				Version: controllerVersion,
			},
		),
	)
	Expect(err).ToNot(HaveOccurred())
	Expect(reconciler).ToNot(BeNil())

	Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctrl.SetupSignalHandler())).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	close(syncCh)
	testEnv.Stop()
})
