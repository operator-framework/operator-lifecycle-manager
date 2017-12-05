package e2e

import (
	"flag"
	"os"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"encoding/json"

	"github.com/coreos-inc/alm/pkg/apis"
	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	installplanv1alpha1 "github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	"github.com/coreos-inc/alm/pkg/install"
	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
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
			ClusterServiceVersionNames: []string{"vault-operator.0.1.3"},
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

func TestUpdateClusterServiceVersion(t *testing.T) {
	c := newKubeClient(t)

	strategyPrev := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: "prev-deployment-keep",
				Spec: v1beta1.DeploymentSpec{
					Template: v1.PodTemplateSpec{
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  "test-1",
									Image: "hello-world",
								},
							},
						},
					},
				},
			},
			{
				Name: "prev-deployment-delete",
				Spec: v1beta1.DeploymentSpec{
					Template: v1.PodTemplateSpec{
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  "test-2",
									Image: "hello-world",
								},
							},
						},
					},
				},
			},
		},
	}

	jsonStrategyPrev := json.Marshal(prev)

	prevCSV := csvv1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       csvv1alpha1.ClusterServiceVersionKind,
			APIVersion: csvv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clusterservice-0.0.1",
			Namespace: testNamespace,
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			InstallStrategy: csvv1alpha1.NamedInstallStrategy{
				StrategyName:    install.InstallStrategyNameDeployment,
				StrategySpecRaw: json.RawMessage{},
			},
			Version: *semver.New("0.0.1"),
		},
	}
}
