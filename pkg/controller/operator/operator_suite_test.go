package operator

import (
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage/names"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	mgrClient client.Client
	mgr       ctrl.Manager
	testEnv   *envtest.Environment

	scheme            = runtime.NewScheme()
	log               = ctrl.Log.WithName("operator-test-suite")
	gracePeriod int64 = 0
	propagation       = metav1.DeletePropagationForeground
	deleteOpts        = &client.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &propagation,
	}
	genName = names.SimpleNameGenerator.GenerateName
)

const (
	timeout  = time.Second * 5
	interval = time.Millisecond * 100
)

func TestOperator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{envtest.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	ctrl.SetLogger(zap.LoggerTo(GinkgoWriter, true))

	// Set up k8s test env
	By("Bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "..", "manifests"), // TODO(njhale): Remove once all CRDs are present in the config directory.
		},
	}

	cfg, err := testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	By("Setting up a controller manager")
	mgr, err = ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme})
	Expect(err).ToNot(HaveOccurred())

	By("Adding the operator controller to the manager")
	err = AddController(mgr, log)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()

		By("Starting managed controllers")
		err = mgr.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

	mgrClient = mgr.GetClient()
	Expect(mgrClient).ToNot(BeNil())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	gexec.KillAndWait(5 * time.Second)
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})
