package e2e

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/coreos-inc/alm/pkg/apis"
	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	installplanv1alpha1 "github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	uicatalogentryv1alpha1 "github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
	"github.com/coreos-inc/alm/pkg/registry"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	conversion "k8s.io/apimachinery/pkg/conversion/unstructured"
)

const (
	expectedUICatalogEntries = 3
	vaultVersion             = "0.9.0-0"
	expectedEtcdNodes        = 3
	vaultClusterSize         = 2
	ocsConfigMap             = "tectonic-ocs"
)

type installPlanConditionChecker func(fip *installplanv1alpha1.InstallPlan) bool

var installPlanCompleteChecker = func(fip *installplanv1alpha1.InstallPlan) bool {
	return fip.Status.Phase == installplanv1alpha1.InstallPlanPhaseComplete
}

var installPlanFailedChecker = func(fip *installplanv1alpha1.InstallPlan) bool {
	return fip.Status.Phase == installplanv1alpha1.InstallPlanPhaseFailed
}

func FetchUICatalogEntries(t *testing.T, c opClient.Interface, count int) (*opClient.CustomResourceList, error) {
	var crl *opClient.CustomResourceList
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		crl, err = c.ListCustomResource(apis.GroupName, uicatalogentryv1alpha1.GroupVersion, testNamespace, uicatalogentryv1alpha1.UICatalogEntryKind)

		if err != nil {
			return false, err
		}

		if len(crl.Items) < count {
			t.Logf("waiting for %d entries, %d present", count, len(crl.Items))
			return false, nil
		}

		return true, nil
	})

	return crl, err
}

func buildInstallPlanCleanupFunc(c opClient.Interface, installPlan *installplanv1alpha1.InstallPlan) cleanupFunc {
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

func decorateCommonAndCreateInstallPlan(c opClient.Interface, plan installplanv1alpha1.InstallPlan) (cleanupFunc, error) {
	plan.Kind = installplanv1alpha1.InstallPlanKind
	plan.APIVersion = installplanv1alpha1.SchemeGroupVersion.String()
	plan.Namespace = testNamespace
	unstructuredConverter := conversion.NewConverter(true)
	ipUnst, err := unstructuredConverter.ToUnstructured(&plan)
	if err != nil {
		return nil, err
	}
	err = c.CreateCustomResource(&unstructured.Unstructured{Object: ipUnst})
	if err != nil {
		return nil, err
	}
	return buildInstallPlanCleanupFunc(c, &plan), nil
}

func fetchInstallPlan(t *testing.T, c opClient.Interface, name string, checker installPlanConditionChecker) (*installplanv1alpha1.InstallPlan, error) {
	var fetchedInstallPlan *installplanv1alpha1.InstallPlan
	var err error

	unstructuredConverter := conversion.NewConverter(true)
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, installplanv1alpha1.InstallPlanKind, name)
		if err != nil {
			return false, err
		}

		err = unstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, &fetchedInstallPlan)
		require.NoError(t, err)

		return checker(fetchedInstallPlan), nil
	})

	return fetchedInstallPlan, err
}

// This test is skipped until manual approval is implemented
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
		},
	}

	// Create a new installplan for vault with manual approval
	cleanup, err := decorateCommonAndCreateInstallPlan(c, vaultInstallPlan)
	require.NoError(t, err)
	defer cleanup()

	// Get InstallPlan and verify status
	fetchedInstallPlan, err := fetchInstallPlan(t, c, vaultInstallPlan.GetName(), installPlanCompleteChecker)
	require.NoError(t, err)
	require.Equal(t, installplanv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	vaultResourcesPresent := 0

	// Step through the InstallPlan and check if resources have been created
	for _, step := range fetchedInstallPlan.Status.Plan {
		t.Logf("Verifiying that %s %s is not present", step.Resource.Kind, step.Resource.Name)
		if step.Resource.Kind == "CustomResourceDefinition" {
			_, err := c.GetCustomResourceDefinition(step.Resource.Name)

			require.NoError(t, err)
			vaultResourcesPresent++
		} else if step.Resource.Kind == "ClusterServiceVersion-v1" {
			_, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, step.Resource.Kind, step.Resource.Name)

			require.NoError(t, err)
			vaultResourcesPresent++
		} else if step.Resource.Kind == "Secret" {
			_, err := c.KubernetesInterface().CoreV1().Secrets(testNamespace).Get(step.Resource.Name, metav1.GetOptions{})

			require.NoError(t, err)
		}
	}

	// Result: Ensure that the InstallPlan actually creates no vault resources
	t.Skip()
	t.Logf("%d Vault Resources present", vaultResourcesPresent)
	require.Zero(t, vaultResourcesPresent)
}

func TestUICatalogEntriesPresent(t *testing.T) {
	c := newKubeClient(t)

	requiredUICatalogEntryNames := []string{"etcd", "prometheus", "vault"}

	var fetchedUICatalogEntryNames *opClient.CustomResourceList

	// This test may start before all of the UICatalogEntries are present in the cluster
	fetchedUICatalogEntryNames, err := FetchUICatalogEntries(t, c, len(requiredUICatalogEntryNames))
	require.NoError(t, err)

	uiCatalogEntryNames := make([]string, len(fetchedUICatalogEntryNames.Items))
	for i, uicName := range fetchedUICatalogEntryNames.Items {
		uiCatalogEntryNames[i] = strings.Split(uicName.GetName(), ".")[0]
	}

	for _, name := range requiredUICatalogEntryNames {
		require.Contains(t, uiCatalogEntryNames, name)
	}

}

// TestUICatalogEntriesVisibility tests that the visibility is copied over from CSV to catalog entry
func TestUICatalogEntriesVisibility(t *testing.T) {
	c := newKubeClient(t)

	requiredVisibilities := map[string]string{
		"etcd":       "ocs",
		"prometheus": "ocs",
		"vault":      "ocs",
	}

	// This test may start before all of the UICatalogEntries are present in the cluster
	fetchedUICatalogEntries, err := FetchUICatalogEntries(t, c, len(requiredVisibilities))
	require.NoError(t, err)

	for _, entry := range fetchedUICatalogEntries.Items {
		serviceName := strings.Split(entry.GetName(), ".")[0] // remove version info
		labels := entry.GetLabels()

		actual, ok := labels["tectonic-visibility"]
		require.True(t, ok, "missing visibility label: service='%s' labels=%v", serviceName, labels)

		expected := requiredVisibilities[serviceName]
		require.Equal(t, expected, actual, "incorrect visibility: service='%s'", serviceName)
	}
}

func TestCreateInstallPlanFromEachUICatalogEntry(t *testing.T) {
	c := newKubeClient(t)

	fetchedUICatalogEntryNames, err := FetchUICatalogEntries(t, c, expectedUICatalogEntries)
	require.NoError(t, err)

	unstructuredConverter := conversion.NewConverter(true)
	for _, uic := range fetchedUICatalogEntryNames.Items {
		catalogEntry := uicatalogentryv1alpha1.UICatalogEntry{}
		unstructuredConverter.FromUnstructured(uic.Object, &catalogEntry)
		csvName := catalogEntry.Spec.Manifest.Channels[0].CurrentCSVName

		t.Logf("Creating install plan for %s", csvName)
		installPlan := installplanv1alpha1.InstallPlan{
			TypeMeta: metav1.TypeMeta{
				Kind:       installplanv1alpha1.InstallPlanKind,
				APIVersion: installplanv1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("install-%s", csvName),
				Namespace: testNamespace,
			},
			Spec: installplanv1alpha1.InstallPlanSpec{
				ClusterServiceVersionNames: []string{csvName},
				Approval:                   installplanv1alpha1.ApprovalAutomatic,
			},
		}

		unstructuredInstallPlan, err := unstructuredConverter.ToUnstructured(&installPlan)
		require.NoError(t, err)

		err = c.CreateCustomResource(&unstructured.Unstructured{Object: unstructuredInstallPlan})
		require.NoError(t, err)

		// Wait for InstallPlan to be status: Complete before checking for resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, c, installPlan.GetName(), installPlanCompleteChecker)

		require.NoError(t, err)
		require.Equal(t, fetchedInstallPlan.Status.Phase, installplanv1alpha1.InstallPlanPhaseComplete)

		crdsPresent := 0
		csvsPresent := 0

		// Ensure that each component of the InstallPlan is present in the cluster
		// Currently only checking for CustomResourceDefinitions, ClusterServiceVersion-v1s and Secrets
		for _, step := range fetchedInstallPlan.Status.Plan {
			t.Logf("Verifiying that %s %s is present", step.Resource.Kind, step.Resource.Name)
			if step.Resource.Kind == "CustomResourceDefinition" {
				_, err := c.GetCustomResourceDefinition(step.Resource.Name)

				require.NoError(t, err)
				crdsPresent++
			} else if step.Resource.Kind == "ClusterServiceVersion-v1" {
				_, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, step.Resource.Kind, step.Resource.Name)

				require.NoError(t, err)
				csvsPresent++
			} else if step.Resource.Kind == "Secret" {
				_, err := c.KubernetesInterface().CoreV1().Secrets(testNamespace).Get(step.Resource.Name, metav1.GetOptions{})

				require.NoError(t, err)
			}

		}

		// Ensure that the InstallPlan actually has at least one CRD and CSV
		t.Logf("%d CRDs present for %s", crdsPresent, csvName)
		require.NotEmpty(t, crdsPresent)
		t.Logf("%d CSVs present for %s", csvsPresent, csvName)
		require.NotEmpty(t, csvsPresent)
	}
}

// This captures the current state of ALM where Failed InstallPlans aren't implemented and should be removed in the future
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
	unstructuredConverter := conversion.NewConverter(true)
	unstructuredInstallPlan, err := unstructuredConverter.ToUnstructured(&installPlan)
	require.NoError(t, err)

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: unstructuredInstallPlan})
	require.NoError(t, err)

	fetchedInstallPlan, err := fetchInstallPlan(t, c, installPlan.GetName(), func(fip *installplanv1alpha1.InstallPlan) bool {
		return fip.Status.Phase == installplanv1alpha1.InstallPlanPhasePlanning &&
			fip.Status.Conditions[0].Type == installplanv1alpha1.InstallPlanResolved &&
			fip.Status.Conditions[0].Reason == installplanv1alpha1.InstallPlanReasonDependencyConflict
	})

	// InstallPlans don't have a failed status, they end up in a Planning state with a "false" resolved state
	require.Equal(t, fetchedInstallPlan.Status.Conditions[0].Type, installplanv1alpha1.InstallPlanResolved)
	require.Equal(t, fetchedInstallPlan.Status.Conditions[0].Status, corev1.ConditionFalse)
	require.Equal(t, fetchedInstallPlan.Status.Conditions[0].Reason, installplanv1alpha1.InstallPlanReasonDependencyConflict)

}

// As an infra owner, creating an installplan with a clusterServiceVersionName that does not exist in the catalog should result in a “Failed” status
func TestCreateInstallPlanFromInvalidClusterServiceVersionName(t *testing.T) {
	t.Skip("InstallPlanPhaseFailed isn't implemented yet")

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
	unstructuredConverter := conversion.NewConverter(true)
	unstructuredInstallPlan, err := unstructuredConverter.ToUnstructured(&installPlan)
	require.NoError(t, err)

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: unstructuredInstallPlan})
	require.NoError(t, err)

	// Wait for InstallPlan to be status: Complete before checking for resource presence
	fetchedInstallPlan, err := fetchInstallPlan(t, c, installPlan.GetName(), installPlanFailedChecker)
	require.NoError(t, err)

	require.Equal(t, fetchedInstallPlan.Status.Phase, installplanv1alpha1.InstallPlanPhaseFailed)
}
