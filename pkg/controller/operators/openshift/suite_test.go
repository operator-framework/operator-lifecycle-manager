package openshift

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/operator-framework/api/crds"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(
		t,
		"OpenShift Suite",
		[]Reporter{printer.NewlineReporter{}},
	)
}

var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
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

	base := filepath.Join("..", "..", "..", "..", "vendor", "github.com", "openshift", "api", "config", "v1")
	testEnv = &envtest.Environment{
		ErrorIfCRDPathMissing: true,
		CRDs: []client.Object{
			crds.ClusterServiceVersion(),
		},
		CRDDirectoryPaths: []string{
			filepath.Join(base, "0000_00_cluster-version-operator_01_clusteroperator.crd.yaml"),
			filepath.Join(base, "0000_00_cluster-version-operator_01_clusterversion.crd.yaml"),
		},
	}

	cfg, err := testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	ctx = context.Background()
	now := metav1.Now().Rfc3339Copy()
	fixedNow = func() metav1.Time {
		return now
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		MetricsBindAddress: "0", // Prevents conflicts with other test suites that might bind metrics
	})
	Expect(err).ToNot(HaveOccurred())

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
	Expect(testEnv.Stop()).To(Succeed())
})
