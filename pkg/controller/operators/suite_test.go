package operators

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/reference"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// +kubebuilder:scaffold:imports

	"github.com/operator-framework/api/crds"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/decorators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/testobj"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	timeout  = time.Second * 20
	interval = time.Millisecond * 100
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context

	scheme            = runtime.NewScheme()
	gracePeriod int64 = 1
	propagation       = metav1.DeletePropagationForeground
	deleteOpts        = &client.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &propagation,
	}
	genName  = names.SimpleNameGenerator.GenerateName
	fixtures = testobj.NewFixtureFiller(
		testobj.WithFixtureFile(&appsv1.Deployment{}, "testdata/fixtures/deployment.yaml"),
		testobj.WithFixtureFile(&corev1.Service{}, "testdata/fixtures/service.yaml"),
		testobj.WithFixtureFile(&corev1.ServiceAccount{}, "testdata/fixtures/sa.yaml"),
		testobj.WithFixtureFile(&corev1.Secret{}, "testdata/fixtures/secret.yaml"),
		testobj.WithFixtureFile(&corev1.ConfigMap{}, "testdata/fixtures/configmap.yaml"),
		testobj.WithFixtureFile(&rbacv1.Role{}, "testdata/fixtures/role.yaml"),
		testobj.WithFixtureFile(&rbacv1.RoleBinding{}, "testdata/fixtures/rb.yaml"),
		testobj.WithFixtureFile(&rbacv1.ClusterRole{}, "testdata/fixtures/clusterrole.yaml"),
		testobj.WithFixtureFile(&rbacv1.ClusterRoleBinding{}, "testdata/fixtures/crb.yaml"),
		testobj.WithFixtureFile(&apiextensionsv1.CustomResourceDefinition{}, "testdata/fixtures/crd.yaml"),
		testobj.WithFixtureFile(&apiregistrationv1.APIService{}, "testdata/fixtures/apiservice.yaml"),
		testobj.WithFixtureFile(&operatorsv1alpha1.InstallPlan{}, "testdata/fixtures/installplan.yaml"),
	)
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(
		t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}},
	)
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	By("bootstrapping test environment")
	useExisting := false
	testEnv = &envtest.Environment{
		UseExistingCluster: &useExisting,
		CRDs: []client.Object{
			crds.CatalogSource(),
			crds.ClusterServiceVersion(),
			crds.InstallPlan(),
			crds.Subscription(),
			crds.OperatorGroup(),
			crds.Operator(),
			crds.OperatorCondition(),
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	By("Setting up a controller manager")
	err = AddToScheme(scheme)
	Expect(err).ToNot(HaveOccurred())
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		MetricsBindAddress: "0", // Prevents conflicts with other test suites that might bind metrics
		Scheme:             scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	operatorReconciler, err := NewOperatorReconciler(
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName("Operator"),
		mgr.GetScheme(),
	)
	Expect(err).ToNot(HaveOccurred())

	adoptionReconciler, err := NewAdoptionReconciler(
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName("Adoption"),
		mgr.GetScheme(),
	)
	Expect(err).ToNot(HaveOccurred())

	operatorConditionReconciler, err := NewOperatorConditionReconciler(
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName("OperatorCondition"),
		mgr.GetScheme(),
	)
	Expect(err).ToNot(HaveOccurred())

	operatorConditionGeneratorReconciler, err := NewOperatorConditionGeneratorReconciler(
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName("OperatorCondition"),
		mgr.GetScheme(),
	)
	Expect(err).ToNot(HaveOccurred())

	By("Adding controllers to the manager")
	Expect(operatorReconciler.SetupWithManager(mgr)).ToNot(HaveOccurred())
	Expect(adoptionReconciler.SetupWithManager(mgr)).ToNot(HaveOccurred())
	Expect(operatorConditionReconciler.SetupWithManager(mgr)).ToNot(HaveOccurred())
	Expect(operatorConditionGeneratorReconciler.SetupWithManager(mgr)).ToNot(HaveOccurred())

	ctx = ctrl.SetupSignalHandler()
	go func() {
		defer GinkgoRecover()

		By("Starting managed controllers")
		err := mgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred())
	}()

	Expect(mgr.GetCache().WaitForCacheSync(ctx)).To(BeTrue(), "Cache sync failed on startup")

	k8sClient = mgr.GetClient()
	Expect(k8sClient).ToNot(BeNil())
}, 60)

var _ = AfterSuite(func() {
	By("stopping the controller manager")
	ctx.Done()

	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})

func newOperator(name string) *decorators.Operator {
	return &decorators.Operator{
		Operator: &operatorsv1.Operator{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		},
	}
}

func toRefs(scheme *runtime.Scheme, objs ...runtime.Object) (refs []operatorsv1.RichReference) {
	for _, obj := range objs {
		ref, err := reference.GetReference(scheme, obj)
		if err != nil {
			panic(fmt.Errorf("error creating resource reference: %v", err))
		}

		// Clear unnecessary fields
		ref.UID = ""
		ref.ResourceVersion = ""

		refs = append(refs, operatorsv1.RichReference{
			ObjectReference: ref,
		})
	}

	return
}
