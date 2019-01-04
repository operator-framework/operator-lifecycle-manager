//  +build !bare

package e2e

import (
	"fmt"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

func TestCatalogLoadingBetweenRestarts(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	// create a simple catalogsource
	packageName := genName("nginx")
	stableChannel := "stable"
	packageStable := packageName + "-stable"
	manifests := []registry.PackageManifest{
		{
			PackageName: packageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: packageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"
	crd := newCRD(crdName, crdPlural)
	namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	csv := newCSV(packageStable, operatorNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{crd}, nil, namedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)

	catalogSourceName := genName("mock-ocs")
	_, cleanupCatalogSource, err := createInternalCatalogSource(t, c, crc, catalogSourceName, operatorNamespace, manifests, []extv1beta1.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
	require.NoError(t, err)
	defer cleanupCatalogSource()

	// ensure the mock catalog exists and has been synced by the catalog operator
	catalogSource, err := fetchCatalogSource(t, crc, catalogSourceName, operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// get catalog operator deployment
	deployment, err := getOperatorDeployment(c, operatorNamespace, labels.Set{"app": "catalog-operator"})
	require.NoError(t, err)
	require.NotNil(t, deployment, "Could not find catalog operator deployment")

	// rescale catalog operator
	t.Log("Rescaling catalog operator...")
	err = rescaleDeployment(c, deployment)
	require.NoError(t, err, "Could not rescale catalog operator")
	t.Log("Catalog operator rescaled")

	// check for last synced update to catalogsource
	t.Log("Checking for catalogsource lastSync updates")
	_, err = fetchCatalogSource(t, crc, catalogSourceName, operatorNamespace, func(cs *v1alpha1.CatalogSource) bool {
		if cs.Status.LastSync.After(catalogSource.Status.LastSync.Time) {
			t.Logf("lastSync updated: %s -> %s", catalogSource.Status.LastSync, cs.Status.LastSync)
			return true
		}
		return false
	})
	require.NoError(t, err, "Catalog source never loaded into memory after catalog operator rescale")
	t.Logf("Catalog source sucessfully loaded after rescale")
}

func TestDefaultCatalogLoading(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)
	c := newKubeClient(t)
	crc := newCRClient(t)

	catalogSource, err := fetchCatalogSource(t, crc, "rh-operators", operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	requirement, err := labels.NewRequirement("olm.catalogSource", selection.Equals, []string{catalogSource.GetName()})
	require.NoError(t, err)
	selector := labels.NewSelector().Add(*requirement).String()
	pods, err := c.KubernetesInterface().CoreV1().Pods(operatorNamespace).List(metav1.ListOptions{LabelSelector: selector})
	require.NoError(t, err)
	for _, p := range pods.Items {
		for _, s := range p.Status.ContainerStatuses {
			require.True(t, s.Ready)
			require.Zero(t, s.RestartCount)
		}
	}
}

func getOperatorDeployment(c operatorclient.ClientInterface, namespace string, operatorLabels labels.Set) (*appsv1.Deployment, error) {
	deployments, err := c.ListDeploymentsWithLabels(namespace, operatorLabels)
	if err != nil || deployments == nil || len(deployments.Items) != 1 {
		return nil, fmt.Errorf("Error getting single operator deployment for label: %v", operatorLabels)
	}
	return &deployments.Items[0], nil
}

func rescaleDeployment(c operatorclient.ClientInterface, deployment *appsv1.Deployment) error {
	// scale down
	var replicas int32 = 0
	deployment.Spec.Replicas = &replicas
	deployment, updated, err := c.UpdateDeployment(deployment)
	if err != nil || updated == false || deployment == nil {
		return fmt.Errorf("Failed to scale down deployment")
	}

	waitForScaleup := func() (bool, error) {
		fetchedDeployment, err := c.GetDeployment(deployment.GetNamespace(), deployment.GetName())
		if err != nil {
			return true, err
		}
		if fetchedDeployment.Status.Replicas == replicas {
			return true, nil
		}

		return false, nil
	}

	// wait for deployment to scale down
	err = wait.Poll(pollInterval, pollDuration, waitForScaleup)
	if err != nil {
		return err
	}

	// scale up
	replicas = 1
	deployment.Spec.Replicas = &replicas
	deployment, updated, err = c.UpdateDeployment(deployment)
	if err != nil || updated == false || deployment == nil {
		return fmt.Errorf("Failed to scale up deployment")
	}

	// wait for deployment to scale up
	err = wait.Poll(pollInterval, pollDuration, waitForScaleup)

	return err
}
