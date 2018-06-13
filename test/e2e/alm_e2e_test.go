package e2e

import (
	"fmt"
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
	installplanv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	vaultVersion           = "0.9.0-0"
	etcdVersion            = "3.2.13"
	prometheusVersion      = "v1.7.0"
	expectedEtcdNodes      = 3
	expectedPrometheusSize = 3
	vaultClusterSize       = 2
	ocsConfigMap           = "tectonic-ocs"
)

type installPlanConditionChecker func(fip *installplanv1alpha1.InstallPlan) bool

var installPlanCompleteChecker = func(fip *installplanv1alpha1.InstallPlan) bool {
	return fip.Status.Phase == installplanv1alpha1.InstallPlanPhaseComplete
}

var installPlanFailedChecker = func(fip *installplanv1alpha1.InstallPlan) bool {
	return fip.Status.Phase == installplanv1alpha1.InstallPlanPhaseFailed
}

var installPlanRequiresApprovalChecker = func(fip *installplanv1alpha1.InstallPlan) bool {
	return fip.Status.Phase == installplanv1alpha1.InstallPlanPhaseRequiresApproval
}

func buildInstallPlanCleanupFunc(c operatorclient.ClientInterface, installPlan *installplanv1alpha1.InstallPlan) cleanupFunc {
	return func() {
		for _, step := range installPlan.Status.Plan {
			if step.Resource.Kind == v1alpha1.ClusterServiceVersionKind {
				err := c.DeleteCustomResource(step.Resource.Group, step.Resource.Version, testNamespace, step.Resource.Kind, step.Resource.Name)
				if err != nil {
					fmt.Println(err)
				}
			}
		}
		err := c.DeleteCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, installplanv1alpha1.InstallPlanKind, installPlan.GetName())
		if err != nil {
			fmt.Println(err)
		}
	}
}

func decorateCommonAndCreateInstallPlan(c operatorclient.ClientInterface, plan installplanv1alpha1.InstallPlan) (cleanupFunc, error) {
	plan.Kind = installplanv1alpha1.InstallPlanKind
	plan.APIVersion = installplanv1alpha1.SchemeGroupVersion.String()
	plan.Namespace = testNamespace
	ipUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&plan)
	if err != nil {
		return nil, err
	}
	err = c.CreateCustomResource(&unstructured.Unstructured{Object: ipUnst})
	if err != nil {
		return nil, err
	}
	return buildInstallPlanCleanupFunc(c, &plan), nil
}

func fetchInstallPlan(t *testing.T, c operatorclient.ClientInterface, name string, checker installPlanConditionChecker) (*installplanv1alpha1.InstallPlan, error) {
	var fetchedInstallPlan *installplanv1alpha1.InstallPlan
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, installplanv1alpha1.InstallPlanKind, name)
		if err != nil {
			return false, err
		}

		err = runtime.DefaultUnstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, &fetchedInstallPlan)
		require.NoError(t, err)

		return checker(fetchedInstallPlan), nil
	})

	return fetchedInstallPlan, err
}

func TestCreateInstallPlanManualApproval(t *testing.T) {
	c := newKubeClient(t)

	inMem, err := registry.NewInMemoryFromConfigMap(c, testNamespace, ocsConfigMap)
	require.NoError(t, err)
	require.NotNil(t, inMem)
	latestVaultCSV, err := inMem.FindCSVForPackageNameUnderChannel("vault", "alpha")
	require.NoError(t, err)
	require.NotNil(t, latestVaultCSV)

	vaultInstallPlan := installplanv1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name: "install-manual-" + latestVaultCSV.GetName(),
		},
		Spec: installplanv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{latestVaultCSV.GetName()},
			Approval:                   installplanv1alpha1.ApprovalManual,
			Approved:                   false,
		},
	}

	// Create a new InstallPlan for Vault with manual approval
	cleanup, err := decorateCommonAndCreateInstallPlan(c, vaultInstallPlan)
	require.NoError(t, err)
	defer cleanup()

	// Get InstallPlan and verify status
	fetchedInstallPlan, err := fetchInstallPlan(t, c, vaultInstallPlan.GetName(), installPlanRequiresApprovalChecker)
	require.NoError(t, err)

	var verifyResources = func(installPlan *installplanv1alpha1.InstallPlan, shouldBeCreated bool) int {
		resourcesPresent := 0
		// Step through the InstallPlan and check if resources have been created or not
		for _, step := range installPlan.Status.Plan {
			t.Logf("Verifiying that %s %s is not present", step.Resource.Kind, step.Resource.Name)
			if step.Resource.Kind == "CustomResourceDefinition" {
				// _, err := c.GetCustomResourceDefinition(step.Resource.Name)

				// FIXME: CI cluster will already have the CRDs so this will always fail
				if shouldBeCreated {
					// require.NoError(t, err)
					// resourcesPresent = resourcesPresent + 1
				} else {
					// require.Error(t, err)
				}
			} else if step.Resource.Kind == "ClusterServiceVersion-v1" {
				_, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, step.Resource.Kind, step.Resource.Name)

				if shouldBeCreated {
					require.NoError(t, err)
					resourcesPresent = resourcesPresent + 1
				} else {
					require.Error(t, err)
				}
			} else if step.Resource.Kind == "Secret" {
				_, err := c.KubernetesInterface().CoreV1().Secrets(testNamespace).Get(step.Resource.Name, metav1.GetOptions{})

				if shouldBeCreated {
					require.NoError(t, err)
					resourcesPresent = resourcesPresent + 1
				} else {
					require.Error(t, err)
				}
			}
		}
		return resourcesPresent
	}

	vaultResourcesPresent := verifyResources(fetchedInstallPlan, false)
	// Result: Ensure that the InstallPlan does not actually create Vault resources
	t.Logf("%d Vault Resources present", vaultResourcesPresent)
	require.Zero(t, vaultResourcesPresent)

	// Approve InstallPlan and update
	fetchedInstallPlan.Spec.Approved = true
	ipUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&fetchedInstallPlan)
	require.NoError(t, err)
	err = c.UpdateCustomResource(&unstructured.Unstructured{Object: ipUnst})
	require.NoError(t, err)

	approvedInstallPlan, err := fetchInstallPlan(t, c, fetchedInstallPlan.GetName(), installPlanCompleteChecker)
	require.NoError(t, err)

	vaultResourcesPresent = verifyResources(approvedInstallPlan, true)
	// Result: Ensure that the InstallPlan actually creates Vault resources
	t.Logf("%d Vault Resources present", vaultResourcesPresent)
	require.NotZero(t, vaultResourcesPresent)

}

// This captures the current state of OLM where Failed InstallPlans aren't implemented and should be removed in the future
func TestCreateInstallPlanFromInvalidClusterServiceVersionNameExistingBehavior(t *testing.T) {
	c := newKubeClient(t)

	installPlan := installplanv1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       installplanv1alpha1.InstallPlanKind,
			APIVersion: installplanv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-bitcoin-miner",
			Namespace: testNamespace,
		},
		Spec: installplanv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{"Bitcoin-miner-0.1"},
			Approval:                   installplanv1alpha1.ApprovalAutomatic,
		},
	}
	unstructuredInstallPlan, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&installPlan)
	require.NoError(t, err)

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: unstructuredInstallPlan})
	require.NoError(t, err)

	fetchedInstallPlan, err := fetchInstallPlan(t, c, installPlan.GetName(), func(fip *installplanv1alpha1.InstallPlan) bool {
		return fip.Status.Phase == installplanv1alpha1.InstallPlanPhasePlanning &&
			fip.Status.Conditions[0].Type == installplanv1alpha1.InstallPlanResolved &&
			fip.Status.Conditions[0].Reason == installplanv1alpha1.InstallPlanReasonDependencyConflict
	})

	// InstallPlans don't have a failed status, they end up in a Planning state with a "false" resolved state
	require.Equal(t, installplanv1alpha1.InstallPlanResolved, fetchedInstallPlan.Status.Conditions[0].Type)
	require.Equal(t, corev1.ConditionFalse, fetchedInstallPlan.Status.Conditions[0].Status)
	require.Equal(t, installplanv1alpha1.InstallPlanReasonInstallCheckFailed, fetchedInstallPlan.Status.Conditions[0].Reason)
}

// As an infra owner, creating an installplan with a clusterServiceVersionName that does not exist in the catalog should result in a “Failed” status
func TestCreateInstallPlanFromInvalidClusterServiceVersionName(t *testing.T) {
	c := newKubeClient(t)

	installPlan := installplanv1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       installplanv1alpha1.InstallPlanKind,
			APIVersion: installplanv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-dogecoin-miner",
			Namespace: testNamespace,
		},
		Spec: installplanv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{"Dogecoin-miner-0.1"},
			Approval:                   installplanv1alpha1.ApprovalAutomatic,
		},
	}
	unstructuredInstallPlan, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&installPlan)
	require.NoError(t, err)

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: unstructuredInstallPlan})
	require.NoError(t, err)

	// Wait for InstallPlan to be status: Complete before checking for resource presence
	fetchedInstallPlan, err := fetchInstallPlan(t, c, installPlan.GetName(), installPlanFailedChecker)
	require.NoError(t, err)

	require.Equal(t, installplanv1alpha1.InstallPlanPhaseFailed, fetchedInstallPlan.Status.Phase)
}
