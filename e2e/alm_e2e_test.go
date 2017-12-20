package e2e

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/coreos-inc/alm/pkg/apis"
	clusterserviceversionv1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	installplanv1alpha1 "github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	uicatalogentryv1alpha1 "github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	conversion "k8s.io/apimachinery/pkg/conversion/unstructured"
)

const (
	pollInterval             = 1 * time.Second
	pollDuration             = 5 * time.Minute
	expectedUICatalogEntries = 3
	vaultVersion             = "0.9.0-0"
	vaultOperatorVersion     = "0.1.6"
	expectedEtcdNodes        = 3
	vaultClusterSize         = 2
)

var testNamespace = metav1.NamespaceDefault

type ConditionChecker func(fip *installplanv1alpha1.InstallPlan) bool

var InstallPlanCompleteChecker = func(fip *installplanv1alpha1.InstallPlan) bool {
	return fip.Status.Phase == installplanv1alpha1.InstallPlanPhaseComplete
}

var InstallPlanFailedChecker = func(fip *installplanv1alpha1.InstallPlan) bool {
	return fip.Status.Phase == installplanv1alpha1.InstallPlanPhaseFailed
}

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

func FetchInstallPlan(t *testing.T, c opClient.Interface, name string, checker ConditionChecker) (*installplanv1alpha1.InstallPlan, error) {
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

func FetchPods(t *testing.T, c opClient.Interface, selector string, expectedCount int) (*corev1.PodList, error) {
	var fetchedPodList *corev1.PodList
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedPodList, err = c.KubernetesInterface().CoreV1().Pods(testNamespace).List(metav1.ListOptions{
			LabelSelector: selector,
		})

		if err != nil {
			return false, err
		}

		t.Logf("Waiting for %d nodes matching %s selector, %d present", expectedCount, selector, len(fetchedPodList.Items))

		if len(fetchedPodList.Items) < expectedCount {
			return false, nil
		}

		return true, nil
	})

	require.NoError(t, err)
	return fetchedPodList, err
}

func PollForCustomResource(t *testing.T, c opClient.Interface, group string, version string, kind string, name string) error {
	t.Logf("Looking for %s %s in %s\n", kind, name, testNamespace)

	err := wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := c.GetCustomResource(group, version, testNamespace, kind, name)
		if err != nil {
			if sErr := err.(*errors.StatusError); sErr.Status().Reason == metav1.StatusReasonNotFound {
				return false, nil
			}
			return false, err
		}

		return true, nil
	})

	return err
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
			ClusterServiceVersionNames: []string{fmt.Sprintf("vault-operator.%s", vaultOperatorVersion)},
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

func TestCreateInstallPlanFromEachUICatalogEntry(t *testing.T) {
	c := newKubeClient(t)

	fetchedUICatalogEntryNames, err := FetchUICatalogEntries(t, c, expectedUICatalogEntries)
	require.NoError(t, err)

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
		fetchedInstallPlan, err := FetchInstallPlan(t, c, installPlan.GetName(), InstallPlanCompleteChecker)

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
		t.Logf("%d CRDs present for %s", crdsPresent, uiCatalogEntryName)
		require.NotEmpty(t, crdsPresent)
		t.Logf("%d CSVs present for %s", csvsPresent, uiCatalogEntryName)
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

	fetchedInstallPlan, err := FetchInstallPlan(t, c, installPlan.GetName(), func(fip *installplanv1alpha1.InstallPlan) bool {
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
	fetchedInstallPlan, err := FetchInstallPlan(t, c, installPlan.GetName(), InstallPlanFailedChecker)
	require.NoError(t, err)

	require.Equal(t, fetchedInstallPlan.Status.Phase, installplanv1alpha1.InstallPlanPhaseFailed)
}

// As an infra owner, when I create an installplan for vault:
// * I should see a resolved installplan listing EtcdCluster, VaultService, vault ClusterServiceVersion, and etcd ClusterServiceVersion
// * I should see the resolved resources be created in the same namespace.
// * I should see a vault-operator deployment and an etcd-operator deployment in the same namespace
// * I should see service accounts for vault and etcd with permissions matching what’s listed in the ClusterServiceVersions
// * When I create a VaultService CR
//   * I should see a related EtcdCluster CR appear
//   * I should see pods for vault and etcd appear
func TestCreateInstallVaultPlanAndVerifyResources(t *testing.T) {
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
			ClusterServiceVersionNames: []string{fmt.Sprintf("vault-operator.%s", vaultOperatorVersion)},
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
	fetchedInstallPlan, err := FetchInstallPlan(t, c, vaultInstallPlan.GetName(), InstallPlanCompleteChecker)

	require.Equal(t, installplanv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Ensure CustomResourceDefinitions and ClusterServiceVersion-v1s are present in the resolved InstallPlan
	requiredCSVs := []string{"etcdoperator", "vault-operator"}
	requiredCRDs := []string{"etcdclusters.etcd.database.coreos.com", "vaultservices.vault.security.coreos.com"}

	csvNames := map[string]string{}
	crdNames := []string{}

	for _, step := range fetchedInstallPlan.Status.Plan {
		if step.Resource.Kind == "CustomResourceDefinition" {
			crdNames = append(crdNames, step.Resource.Name)
		} else if step.Resource.Kind == clusterserviceversionv1.ClusterServiceVersionKind {
			csvNames[strings.Split(step.Resource.Name, ".")[0]] = step.Resource.Name
		}
	}

	// Check that the CSV and CRDs are actually present in the cluster as well
	for _, name := range requiredCSVs {
		require.NotEmpty(t, csvNames[name])

		t.Logf("Ensuring CSV %s is present in %s namespace", name, testNamespace)
		_, err := c.GetCustomResource(apis.GroupName, installplanv1alpha1.GroupVersion, testNamespace, clusterserviceversionv1.ClusterServiceVersionKind, csvNames[name])
		require.NoError(t, err)
	}

	for _, name := range requiredCRDs {
		require.Contains(t, crdNames, name)

		t.Logf("Ensuring CRD %s is present in cluster", name)
		_, err := c.GetCustomResourceDefinition(name)
		require.NoError(t, err)
	}

	// * I should see service accounts for vault and etcd with permissions matching what’s listed in the ClusterServiceVersions
	for _, accountName := range []string{"etcd-operator", "vault-operator"} {
		var sa *corev1.ServiceAccount
		t.Logf("Looking for ServiceAccount %s in %s\n", accountName, testNamespace)

		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			sa, err = c.GetServiceAccount(testNamespace, accountName)
			if err != nil {
				if sErr := err.(*errors.StatusError); sErr.Status().Reason == metav1.StatusReasonNotFound {
					return false, nil
				}
				return false, err
			}

			return true, nil
		})

		require.NoError(t, err)
		require.Equal(t, accountName, sa.Name)

	}

	for _, deploymentName := range []string{"etcd-operator", "vault-operator"} {
		var deployment *extensions.Deployment
		t.Logf("Looking for Deployment %s in %s\n", deploymentName, testNamespace)

		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			deployment, err = c.GetDeployment(testNamespace, deploymentName)
			if err != nil {
				if sErr := err.(*errors.StatusError); sErr.Status().Reason == metav1.StatusReasonNotFound {
					return false, nil
				}
				return false, err
			}

			return true, nil
		})

		require.NoError(t, err)
		require.Equal(t, deploymentName, deployment.Name)
	}

	// * When I create a VaultService CR
	//   * I should see a related EtcdCluster CR appear
	//   * I should see pods for vault and etcd appear

	// Importing the vault-operator v1alpha1 api package causes all kinds of weird dependency conflicts
	// that I was unable to resolve.
	vaultService := map[string]interface{}{
		"kind":       "VaultService",
		"apiVersion": "vault.security.coreos.com/v1alpha1",
		"metadata": map[string]interface{}{
			"name":      "test-vault",
			"namespace": testNamespace,
		},
		"spec": map[string]interface{}{
			"nodes":   2,
			"version": vaultVersion,
		},
	}

	err = c.CreateCustomResource(&unstructured.Unstructured{Object: vaultService})
	require.NoError(t, err)

	require.NoError(t, PollForCustomResource(t, c, "vault.security.coreos.com", "v1alpha1", "VaultService", "test-vault"))
	require.NoError(t, PollForCustomResource(t, c, "etcd.database.coreos.com", "v1beta2", "EtcdCluster", "test-vault-etcd"))

	etcdPods, err := FetchPods(t, c, "etcd_cluster=test-vault-etcd", expectedEtcdNodes)
	require.NoError(t, err)
	require.Equal(t, expectedEtcdNodes, len(etcdPods.Items))

	vaultPods, err := FetchPods(t, c, "vault_cluster=test-vault", vaultClusterSize)
	require.NoError(t, err)
	require.Equal(t, vaultClusterSize, len(vaultPods.Items))

}
