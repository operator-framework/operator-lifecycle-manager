package openshift

import (
	"fmt"

	semver "github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-registry/pkg/api"
)

var _ = Describe("ClusterOperator controller", func() {
	var (
		clusterOperatorName types.NamespacedName
		cv                  *configv1.ClusterVersion
		csv                 *operatorsv1alpha1.ClusterServiceVersion
	)

	BeforeEach(func() {
		clusterOperatorName = types.NamespacedName{Name: clusterOperator}
		csv = &operatorsv1alpha1.ClusterServiceVersion{
			Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
				InstallStrategy: operatorsv1alpha1.NamedInstallStrategy{
					StrategyName: operatorsv1alpha1.InstallStrategyNameDeployment,
					StrategySpec: operatorsv1alpha1.StrategyDetailsDeployment{
						DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{},
					},
				},
			},
		}
		csv.SetName("olm")
		csv.SetNamespace(controllerNamespace)

		Eventually(func() error {
			return k8sClient.Create(ctx, csv)
		}).Should(Succeed())

	})

	AfterEach(func() {
		Eventually(func() error {
			err := k8sClient.Delete(ctx, csv)
			if err != nil && apierrors.IsNotFound(err) {
				err = nil
			}
			return err
		}).Should(Succeed())
	})

	BeforeEach(func() {
		// "version" singleton is available in OpenShift by default
		cv = &configv1.ClusterVersion{}
		cv.SetName("version")

		Eventually(func() error {
			return k8sClient.Create(ctx, cv)
		}).Should(Succeed())

		cv.Status = configv1.ClusterVersionStatus{
			Desired: configv1.Update{
				Version: clusterVersion,
			},
		}

		Eventually(func() error {
			return k8sClient.Status().Update(ctx, cv)
		}).Should(Succeed())
	})

	AfterEach(func() {
		Eventually(func() error {
			err := k8sClient.Delete(ctx, cv)
			if err != nil && apierrors.IsNotFound(err) {
				err = nil
			}
			return err
		}).Should(Succeed())
	})

	It("should ensure the ClusterOperator always exists", func() {
		By("initally creating it")
		co := &configv1.ClusterOperator{}
		Eventually(func() error {
			return k8sClient.Get(ctx, clusterOperatorName, co)
		}, timeout).Should(Succeed())

		By("recreating it when deleted")
		Eventually(func() error {
			return k8sClient.Delete(ctx, co)
		}).Should(Succeed())

		Eventually(func() error {
			return k8sClient.Get(ctx, clusterOperatorName, co)
		}, timeout).Should(Succeed())
	})

	It("should track related ClusterServiceVersions with the RelatedObjects field", func() {
		co := &configv1.ClusterOperator{}
		Eventually(func() ([]configv1.ObjectReference, error) {
			err := k8sClient.Get(ctx, clusterOperatorName, co)
			return co.Status.RelatedObjects, err
		}, timeout).Should(ConsistOf([]configv1.ObjectReference{
			{
				Group:     operatorsv1alpha1.GroupName,
				Resource:  "clusterserviceversions",
				Namespace: csv.GetNamespace(),
				Name:      csv.GetName(),
			},
		}))
	})

	It("should gate OpenShift upgrades", func() {
		By("setting upgradeable=false before OLM successfully syncs")
		co := &configv1.ClusterOperator{}
		Eventually(func() ([]configv1.ClusterOperatorStatusCondition, error) {
			err := k8sClient.Get(ctx, clusterOperatorName, co)
			return co.Status.Conditions, err
		}, timeout).Should(ContainElement(configv1.ClusterOperatorStatusCondition{
			Type:               configv1.OperatorUpgradeable,
			Status:             configv1.ConditionFalse,
			Message:            "Waiting for updates to take effect",
			LastTransitionTime: fixedNow(),
		}))

		By("setting upgradeable=true after OLM successfully syncs")
		// Signal a successful sync
		syncCh <- nil

		Eventually(func() ([]configv1.ClusterOperatorStatusCondition, error) {
			err := k8sClient.Get(ctx, clusterOperatorName, co)
			return co.Status.Conditions, err
		}, timeout).Should(ContainElement(configv1.ClusterOperatorStatusCondition{
			Type:               configv1.OperatorUpgradeable,
			Status:             configv1.ConditionTrue,
			LastTransitionTime: fixedNow(),
		}))

		By("setting upgradeable=false when incompatible operators exist")

		ns := &corev1.Namespace{}
		ns.SetName("nostromo")

		Eventually(func() error {
			return k8sClient.Create(ctx, ns)
		}).Should(Succeed())
		defer func() {
			Eventually(func() error {
				return k8sClient.Delete(ctx, ns)
			}).Should(Succeed())
		}()

		incompatible := &operatorsv1alpha1.ClusterServiceVersion{Spec: csv.Spec}
		incompatible.SetName("xenomorph")
		incompatible.SetNamespace(ns.GetName())

		withMax := func(version string) map[string]string {
			maxProperty := &api.Property{
				Type:  MaxOpenShiftVersionProperty,
				Value: version,
			}
			value, err := projection.PropertiesAnnotationFromPropertyList([]*api.Property{maxProperty})
			Expect(err).ToNot(HaveOccurred())

			return map[string]string{
				projection.PropertiesAnnotationKey: value,
			}
		}
		incompatible.SetAnnotations(withMax(fmt.Sprintf(`"%s"`, clusterVersion))) // Wrap in quotes so we don't break property marshaling

		Eventually(func() error {
			return k8sClient.Create(ctx, incompatible)
		}).Should(Succeed())
		defer func() {
			Eventually(func() error {
				return k8sClient.Delete(ctx, incompatible)
			}).Should(Succeed())
		}()

		Eventually(func() ([]configv1.ClusterOperatorStatusCondition, error) {
			err := k8sClient.Get(ctx, clusterOperatorName, co)
			return co.Status.Conditions, err
		}, timeout).Should(ContainElement(configv1.ClusterOperatorStatusCondition{
			Type:   configv1.OperatorUpgradeable,
			Status: configv1.ConditionFalse,
			Reason: IncompatibleOperatorsInstalled,
			Message: skews{
				{
					namespace:           ns.GetName(),
					name:                incompatible.GetName(),
					maxOpenShiftVersion: clusterVersion,
				},
			}.String(),
			LastTransitionTime: fixedNow(),
		}))

		By("setting upgradeable=true when incompatible operators become compatible")
		// Set compatibility to the next minor version
		next := semver.MustParse(clusterVersion)
		Expect(next.IncrementMinor()).To(Succeed())
		incompatible.SetAnnotations(withMax(fmt.Sprintf(`"%s"`, next.String())))

		Eventually(func() error {
			return k8sClient.Update(ctx, incompatible)
		}).Should(Succeed())

		Eventually(func() ([]configv1.ClusterOperatorStatusCondition, error) {
			err := k8sClient.Get(ctx, clusterOperatorName, co)
			return co.Status.Conditions, err
		}, timeout).Should(ContainElement(configv1.ClusterOperatorStatusCondition{
			Type:               configv1.OperatorUpgradeable,
			Status:             configv1.ConditionTrue,
			LastTransitionTime: fixedNow(),
		}))

		By("understanding unquoted short max versions; e.g. X.Y")
		// Mimic common pipeline shorthand
		v := semver.MustParse(clusterVersion)
		short := fmt.Sprintf("%d.%d", v.Major, v.Minor)
		incompatible.SetAnnotations(withMax(short))

		Eventually(func() error {
			return k8sClient.Update(ctx, incompatible)
		}).Should(Succeed())

		Eventually(func() ([]configv1.ClusterOperatorStatusCondition, error) {
			err := k8sClient.Get(ctx, clusterOperatorName, co)
			return co.Status.Conditions, err
		}, timeout).Should(ContainElement(configv1.ClusterOperatorStatusCondition{
			Type:   configv1.OperatorUpgradeable,
			Status: configv1.ConditionFalse,
			Reason: IncompatibleOperatorsInstalled,
			Message: skews{
				{
					namespace:           ns.GetName(),
					name:                incompatible.GetName(),
					maxOpenShiftVersion: short + ".0",
				},
			}.String(),
			LastTransitionTime: fixedNow(),
		}))
	})
})
