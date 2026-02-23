package openshift

import (
	"fmt"
	"os"

	semver "github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
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
		os.Setenv(releaseEnvVar, clusterVersion)
	})

	AfterEach(func() {
		resetCurrentReleaseTo(clusterVersion)
	})

	It("should ensure the ClusterOperator always exists", func() {
		By("initially creating it")
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

		By("setting upgradeable=false when there's an error determining compatibility")
		// Reset the ClusterOperator with an invalid version set
		Expect(resetCurrentReleaseTo("")).To(Succeed())
		Eventually(func() error {
			err := k8sClient.Delete(ctx, co)
			if err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			return nil
		}).Should(Succeed())

		Eventually(func() ([]configv1.ClusterOperatorStatusCondition, error) {
			err := k8sClient.Get(ctx, clusterOperatorName, co)
			return co.Status.Conditions, err
		}, timeout).Should(ContainElement(configv1.ClusterOperatorStatusCondition{
			Type:               configv1.OperatorUpgradeable,
			Status:             configv1.ConditionFalse,
			Reason:             ErrorCheckingOperatorCompatibility,
			Message:            fmt.Sprintf("Encountered errors while checking compatibility with the next minor version of OpenShift: desired release version missing from %v env variable", releaseEnvVar),
			LastTransitionTime: fixedNow(),
		}))

		// Reset the ClusterOperator with a valid version set
		Expect(resetCurrentReleaseTo(clusterVersion)).To(Succeed())
		Eventually(func() error {
			err := k8sClient.Delete(ctx, co)
			if err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			return nil
		}).Should(Succeed())

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

		withMax := func(versions ...string) map[string]string {
			var properties []*api.Property
			for _, v := range versions {
				properties = append(properties, &api.Property{
					Type:  MaxOpenShiftVersionProperty,
					Value: v,
				})
			}
			value, err := projection.PropertiesAnnotationFromPropertyList(properties)
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

		parsedVersion := semver.MustParse(clusterVersion)
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
					maxOpenShiftVersion: fmt.Sprintf("%d.%d", parsedVersion.Major, parsedVersion.Minor),
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
					maxOpenShiftVersion: short,
				},
			}.String(),
			LastTransitionTime: fixedNow(),
		}))

		By("setting upgradeable=false when invalid max versions are found")
		incompatible.SetAnnotations(withMax(`"garbage"`))

		Eventually(func() error {
			return k8sClient.Update(ctx, incompatible)
		}).Should(Succeed())

		_, parseErr := semver.ParseTolerant("garbage")
		Eventually(func() ([]configv1.ClusterOperatorStatusCondition, error) {
			err := k8sClient.Get(ctx, clusterOperatorName, co)
			return co.Status.Conditions, err
		}, timeout).Should(ContainElement(configv1.ClusterOperatorStatusCondition{
			Type:   configv1.OperatorUpgradeable,
			Status: configv1.ConditionFalse,
			Reason: IncompatibleOperatorsInstalled,
			Message: skews{
				{
					namespace: ns.GetName(),
					name:      incompatible.GetName(),
					err:       fmt.Errorf(`failed to parse "garbage" as semver: %w`, parseErr),
				},
			}.String(),
			LastTransitionTime: fixedNow(),
		}))

		By("setting upgradeable=false when more than one max version property is defined")
		incompatible.SetAnnotations(withMax(fmt.Sprintf(`"%s"`, clusterVersion), fmt.Sprintf(`"%s"`, next.String())))

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
					namespace: ns.GetName(),
					name:      incompatible.GetName(),
					err:       fmt.Errorf(`defining more than one "%s" property is not allowed`, MaxOpenShiftVersionProperty),
				},
			}.String(),
			LastTransitionTime: fixedNow(),
		}))
	})
})

// resetCurrentRelease thread safely updates the currentRelease.version and then sets the openshift release
// env var to the desired version. WARNING: This function should only be used for testing purposes as it
// goes around the desired logic of only setting the version of the cluster for this operator once.
func resetCurrentReleaseTo(version string) error {
	currentRelease.mu.Lock()
	defer currentRelease.mu.Unlock()

	currentRelease.version = nil
	return os.Setenv(releaseEnvVar, version)
}
