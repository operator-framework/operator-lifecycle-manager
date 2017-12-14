package e2e

import (
	"flag"
	"os"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/pkg/apis"
	installplanv1alpha1 "github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	conversion "k8s.io/apimachinery/pkg/conversion/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute
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
			ClusterServiceVersionNames: []string{"vault-operator.0.1.5"},
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
			ClusterServiceVersionNames: []string{"vault-operator.0.1.5"},
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