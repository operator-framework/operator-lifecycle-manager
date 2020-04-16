package operators

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/api/crds"
	operatorsv2alpha1 "github.com/operator-framework/api/pkg/operators/v2alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/reference"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// +kubebuilder:scaffold:imports

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/decorators"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	timeout  = time.Second * 10
	interval = time.Millisecond * 100
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	stop      chan struct{}

	scheme            = runtime.NewScheme()
	gracePeriod int64 = 0
	propagation       = metav1.DeletePropagationForeground
	deleteOpts        = &client.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &propagation,
	}
	genName = names.SimpleNameGenerator.GenerateName
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.LoggerTo(GinkgoWriter, true))

	By("bootstrapping test environment")
	useExisting := false
	testEnv = &envtest.Environment{
		UseExistingCluster: &useExisting,
		CRDs: []runtime.Object{
			crds.CatalogSource(),
			crds.ClusterServiceVersion(),
			crds.InstallPlan(),
			crds.Subscription(),
			crds.OperatorGroup(),
			crds.Operator(),
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	By("Setting up a controller manager")
	err = AddToScheme(scheme)
	Expect(err).ToNot(HaveOccurred())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme})
	Expect(err).ToNot(HaveOccurred())

	reconciler, err := NewOperatorReconciler(
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName("Operator"),
		mgr.GetScheme(),
	)
	Expect(err).ToNot(HaveOccurred())

	By("Adding the operator controller to the manager")
	Expect(reconciler.SetupWithManager(mgr)).ToNot(HaveOccurred())

	stop = make(chan struct{})
	go func() {
		defer GinkgoRecover()

		By("Starting managed controllers")
		err = mgr.Start(stop)
		Expect(err).ToNot(HaveOccurred())
	}()

	Expect(mgr.GetCache().WaitForCacheSync(stop)).To(BeTrue(), "Cache sync failed on startup")

	k8sClient = mgr.GetClient()
	Expect(k8sClient).ToNot(BeNil())
}, 60)

var _ = AfterSuite(func() {
	By("stopping the controller manager")
	close(stop)

	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})

func newOperator(name string) *decorators.Operator {
	return &decorators.Operator{
		Operator: &operatorsv2alpha1.Operator{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		},
	}
}

func toRefs(scheme *runtime.Scheme, objs ...runtime.Object) (refs []operatorsv2alpha1.RichReference) {
	for _, obj := range objs {
		ref, err := reference.GetReference(scheme, obj)
		if err != nil {
			panic(fmt.Errorf("error creating resource reference: %v", err))
		}

		// Clear unnecessary fields
		ref.UID = ""
		ref.ResourceVersion = ""

		refs = append(refs, operatorsv2alpha1.RichReference{
			ObjectReference: ref,
		})
	}

	return
}
