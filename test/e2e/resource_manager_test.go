package e2e

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("ResourceManager", func() {

	var generatedNamespace corev1.Namespace

	BeforeEach(func() {
		ctx.Ctx().NewE2EClientSession()
		generatedNamespace = SetupGeneratedTestNamespace(genName("resource-manager-e2e-"))
	})

	AfterEach(func() {
		TeardownNamespace(generatedNamespace.GetName())
		Expect(ctx.Ctx().E2EClient().Reset()).To(Succeed())
	})

	It("should tag resources created with it", func() {
		// Create a namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("test-"),
			},
		}
		Expect(ctx.Ctx().E2EClient().Create(context.TODO(), ns)).To(Succeed())

		// Get namespace
		Expect(ctx.Ctx().E2EClient().Get(context.TODO(), client.ObjectKeyFromObject(ns), ns)).To(Succeed())
		Expect(ns.GetAnnotations()).NotTo(BeEmpty())
		Expect(ns.GetAnnotations()[util.E2ETestNameTag]).To(Equal("ResourceManager should tag resources created with it"))
	})

	It("should delete resources on reset", func() {
		// Create a namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("test-"),
			},
		}
		Expect(ctx.Ctx().E2EClient().Create(context.TODO(), ns)).To(Succeed())

		// Add a config map
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("configmap-"),

				// creating the configmap in the generated (spec) namespace
				// so if the namespace (ns, above) gets deleted on reset it won't take the config map with it
				Namespace: generatedNamespace.GetName(),
			},
		}
		Expect(ctx.Ctx().E2EClient().Create(context.TODO(), configMap))

		// Reset the client
		Expect(ctx.Ctx().E2EClient().Reset()).To(Succeed())

		// And just like that resources should be gone
		Eventually(func() error {
			return ctx.Ctx().E2EClient().Get(context.TODO(), client.ObjectKeyFromObject(configMap), configMap)
		}).Should(WithTransform(k8serror.IsNotFound, BeTrue()))
		Eventually(func() error {
			return ctx.Ctx().E2EClient().Get(context.TODO(), client.ObjectKeyFromObject(ns), ns)
		}).Should(WithTransform(k8serror.IsNotFound, BeTrue()))
	})
})
