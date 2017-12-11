package e2e

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/pkg/apis"
	installplanv1alpha1 "github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	uicatalogentryv1alpha1 "github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	conversion "k8s.io/apimachinery/pkg/conversion/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	pollInterval             = 1 * time.Second
	pollDuration             = 5 * time.Minute
	expectedUICatalogEntries = 3
)

var testNamespace = metav1.NamespaceDefault

func init() {
	e2eNamespace := os.Getenv("NAMESPACE")
	if e2eNamespace != "" {
		testNamespace = e2eNamespace
	}
	flag.Set("logtostderr", "true")
	flag.Parse()
}

// newKubeClient configures a client to talk to the cluster defined by KUBECONFIG
func newKubeClient(t *testing.T) opClient.Interface {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	return opClient.NewClient(kubeconfigPath)
}

func TestCreateInstallPlan(t *testing.T) {
	c := newKubeClient(t)

	vaultInstallPlan := installplanv1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       installplanv1alpha1.InstallPlanKind,
			APIVersion: installplanv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-vault",
			Namespace: testNamespace,
		},
		Spec: installplanv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{"vault-operator.0.1.6"},
			Approval:                   installplanv1alpha1.ApprovalAutomatic,
		},
	}

	// Create a new installplan for vault
	unstructuredConverter := conversion.NewConverter(true)
	vaultUnst, err := unstructuredConverter.ToUnstructured(&vaultInstallPlan)
	require.NoError(t, err)
	err = c.CreateCustomResource(&unstructured.Unstructured{Object: vaultUnst})
	require.NoError(t, err)

	// Get InstallPlan and verify status
	fetchedInstallPlan := &installplanv1alpha1.InstallPlan{}
	wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, installplanv1alpha1.InstallPlanKind, vaultInstallPlan.GetName())
		if err != nil {
			return false, err
		}
		err = unstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, fetchedInstallPlan)
		require.NoError(t, err)
		if fetchedInstallPlan.Status.Phase != installplanv1alpha1.InstallPlanPhaseComplete {
			t.Log("waiting for installplan phase to complete")
			return false, nil
		}
		return true, nil
	})
	require.Equal(t, installplanv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	//TODO: poll for creation of other resources
}

// This test is skipped until manual approval is implemented
func TestCreateInstallPlanManualApproval(t *testing.T) {
	c := newKubeClient(t)

	vaultInstallPlan := installplanv1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       installplanv1alpha1.InstallPlanKind,
			APIVersion: installplanv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-vaultmanual",
			Namespace: testNamespace,
		},
		Spec: installplanv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{"vault-operator.0.1.6"},
			Approval:                   installplanv1alpha1.ApprovalManual,
		},
	}

	// Create a new installplan for vault with manual approval
	unstructuredConverter := conversion.NewConverter(true)
	vaultUnst, err := unstructuredConverter.ToUnstructured(&vaultInstallPlan)
	require.NoError(t, err)
	err = c.CreateCustomResource(&unstructured.Unstructured{Object: vaultUnst})
	require.NoError(t, err)

	// Get InstallPlan and verify status
	fetchedInstallPlan := &installplanv1alpha1.InstallPlan{}
	wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, installplanv1alpha1.InstallPlanKind, vaultInstallPlan.GetName())
		if err != nil {
			return false, err
		}
		err = unstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, fetchedInstallPlan)
		require.NoError(t, err)
		if fetchedInstallPlan.Status.Phase != installplanv1alpha1.InstallPlanPhaseComplete {
			t.Log("waiting for installplan phase to complete")
			return false, nil
		}
		return true, nil
	})
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

	requiredUICatalogEntryNames := []string{"etcdoperator", "prometheusoperator", "vault-operator"}

	var fetchedUICatalogEntryNames *opClient.CustomResourceList

	// This test may start before all of the UICatalogEntries are present in the cluster
	wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		var err error

		fetchedUICatalogEntryNames, err = c.ListCustomResource(apis.GroupName, uicatalogentryv1alpha1.GroupVersion, testNamespace, uicatalogentryv1alpha1.UICatalogEntryKind)
		if err != nil {
			return false, err
		}

		if len(fetchedUICatalogEntryNames.Items) < len(requiredUICatalogEntryNames) {
			t.Logf("waiting for %d UICatalogEntry names, %d present", len(requiredUICatalogEntryNames), len(fetchedUICatalogEntryNames.Items))
			return false, nil
		}

		return true, nil
	})

	uiCatalogEntryNames := make([]string, len(fetchedUICatalogEntryNames.Items))
	for i, uicName := range fetchedUICatalogEntryNames.Items {
		uiCatalogEntryNames[i] = strings.Split(uicName.GetName(), ".")[0]
	}

	for _, name := range requiredUICatalogEntryNames {
		require.Contains(t, uiCatalogEntryNames, name)
	}

}

func TestCreateInstallPlanFromEachUICatalogEntry(t *testing.T) {
	c := newKubeClient(t)
	var fetchedUICatalogEntryNames *opClient.CustomResourceList

	// This test may start before all of the UICatalogEntries are present in the cluster
	wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		var err error

		fetchedUICatalogEntryNames, err = c.ListCustomResource(apis.GroupName, uicatalogentryv1alpha1.GroupVersion, testNamespace, uicatalogentryv1alpha1.UICatalogEntryKind)
		if err != nil {
			return false, err
		}

		if len(fetchedUICatalogEntryNames.Items) < expectedUICatalogEntries {
			t.Logf("waiting for %d UICatalogEntries, %d present", expectedUICatalogEntries, len(fetchedUICatalogEntryNames.Items))
			return false, nil
		}

		return true, nil
	})

	unstructuredConverter := conversion.NewConverter(true)
	for _, uic := range fetchedUICatalogEntryNames.Items {
		uiCatalogEntryName := uic.GetName()

		t.Logf("Creating install plan for %s\n", uiCatalogEntryName)

		installPlan := installplanv1alpha1.InstallPlan{
			TypeMeta: metav1.TypeMeta{
				Kind:       installplanv1alpha1.InstallPlanKind,
				APIVersion: installplanv1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("install-%s", uiCatalogEntryName),
				Namespace: testNamespace,
			},
			Spec: installplanv1alpha1.InstallPlanSpec{
				ClusterServiceVersionNames: []string{uiCatalogEntryName},
				Approval:                   installplanv1alpha1.ApprovalAutomatic,
			},
		}

		unstructuredInstallPlan, err := unstructuredConverter.ToUnstructured(&installPlan)
		require.NoError(t, err)

		err = c.CreateCustomResource(&unstructured.Unstructured{Object: unstructuredInstallPlan})
		require.NoError(t, err)

		// Wait for InstallPlan to be status: Complete before checking for resource presence
		fetchedInstallPlan := &installplanv1alpha1.InstallPlan{}
		wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, installplanv1alpha1.InstallPlanKind, installPlan.GetName())
			if err != nil {
				return false, err
			}

			err = unstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, fetchedInstallPlan)
			require.NoError(t, err)
			if fetchedInstallPlan.Status.Phase != installplanv1alpha1.InstallPlanPhaseComplete {
				t.Log("waiting for installplan phase to complete")
				return false, nil
			}
			return true, nil
		})

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
		t.Logf("%d CRDs present for %s", crdsPresent, uiCatalogEntryName)
		require.NotEmpty(t, crdsPresent)
		t.Logf("%d CSVs present for %s", csvsPresent, uiCatalogEntryName)
		require.NotEmpty(t, csvsPresent)
	}
}

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

	fetchedInstallPlan := &installplanv1alpha1.InstallPlan{}
	wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, installplanv1alpha1.InstallPlanKind, installPlan.GetName())
		if err != nil {
			return false, err
		}

		err = unstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, fetchedInstallPlan)
		require.NoError(t, err)

		if fetchedInstallPlan.Status.Phase == installplanv1alpha1.InstallPlanPhasePlanning && (fetchedInstallPlan.Status.Conditions[0].Type != installplanv1alpha1.InstallPlanResolved || fetchedInstallPlan.Status.Conditions[0].Reason != installplanv1alpha1.InstallPlanReasonDependencyConflict) {
			t.Log("waiting for installplan phase to fail")
			return false, nil
		}
		return true, nil
	})

	// InstallPlans don't have a failed status, they end up in a Planning state with a "false" resolved state
	require.Equal(t, fetchedInstallPlan.Status.Conditions[0].Type, installplanv1alpha1.InstallPlanResolved)
	require.Equal(t, fetchedInstallPlan.Status.Conditions[0].Status, corev1.ConditionFalse)
	require.Equal(t, fetchedInstallPlan.Status.Conditions[0].Reason, installplanv1alpha1.InstallPlanReasonDependencyConflict)

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

	fetchedInstallPlan := &installplanv1alpha1.InstallPlan{}
	wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, installplanv1alpha1.InstallPlanKind, installPlan.GetName())
		if err != nil {
			return false, err
		}

		err = unstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, fetchedInstallPlan)
		require.NoError(t, err)

		if fetchedInstallPlan.Status.Phase != installplanv1alpha1.InstallPlanPhaseFailed {
			t.Log("waiting for installplan phase to fail")
			return false, nil
		}
		return true, nil
	})

	require.Equal(t, fetchedInstallPlan.Status.Phase, installplanv1alpha1.InstallPlanPhaseFailed)
}
