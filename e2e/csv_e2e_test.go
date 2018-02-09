package e2e

import (
	"testing"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"

	"encoding/json"

	"fmt"

	"github.com/coreos-inc/alm/pkg/apis"
	"github.com/coreos-inc/alm/pkg/install"
	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	conversion "k8s.io/apimachinery/pkg/conversion/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
)

var singleInstance = int32(1)

type cleanupFunc func()

var immediateDeleteGracePeriod int64 = 0

func buildCSVCleanupFunc(c opClient.Interface, csv v1alpha1.ClusterServiceVersion) cleanupFunc {
	return func() {
		err := c.DeleteCustomResource(apis.GroupName, v1alpha1.GroupVersion, testNamespace, v1alpha1.ClusterServiceVersionKind, csv.GetName())
		if err != nil {
			fmt.Println(err)
		}
	}
}

func createCSV(c opClient.Interface, csv v1alpha1.ClusterServiceVersion) (cleanupFunc, error) {
	csv.Kind = v1alpha1.ClusterServiceVersionKind
	csv.APIVersion = v1alpha1.SchemeGroupVersion.String()
	csv.Namespace = testNamespace
	unstructuredConverter := conversion.NewConverter(true)
	csvUnst, err := unstructuredConverter.ToUnstructured(&csv)
	if err != nil {
		return nil, err
	}
	err = c.CreateCustomResource(&unstructured.Unstructured{Object: csvUnst})
	if err != nil {
		return nil, err
	}
	return buildCSVCleanupFunc(c, csv), nil

}

func buildCRDCleanupFunc(c opClient.Interface, crd extv1beta1.CustomResourceDefinition) cleanupFunc {
	return func() {
		err := c.DeleteCustomResourceDefinition(crd.Name, &metav1.DeleteOptions{GracePeriodSeconds: &immediateDeleteGracePeriod})
		if err != nil {
			fmt.Println(err)
		}
	}
}

func createCRD(c opClient.Interface, crd extv1beta1.CustomResourceDefinition) (cleanupFunc, error) {
	err := c.CreateCustomResourceDefinition(&crd)
	if err != nil {
		return nil, err
	}
	return buildCRDCleanupFunc(c, crd), nil

}

func newNginxDeployment() v1beta1.DeploymentSpec {
	return v1beta1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": "nginx",
			},
		},
		Replicas: &singleInstance,
		Template: v1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "nginx",
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  genName("nginx"),
						Image: "nginx:1.7.9",
						Ports: []v1.ContainerPort{
							{
								ContainerPort: 80,
							},
						},
					},
				},
			},
		},
	}
}

type csvConditionChecker func(csv *v1alpha1.ClusterServiceVersion) bool

var csvPendingChecker = func(csv *v1alpha1.ClusterServiceVersion) bool {
	return csv.Status.Phase == v1alpha1.CSVPhasePending
}

var csvSucceededChecker = func(csv *v1alpha1.ClusterServiceVersion) bool {
	return csv.Status.Phase == v1alpha1.CSVPhaseSucceeded
}

var csvReplacingChecker = func(csv *v1alpha1.ClusterServiceVersion) bool {
	return csv.Status.Phase == v1alpha1.CSVPhaseReplacing || csv.Status.Phase == v1alpha1.CSVPhaseDeleting
}

func fetchCSV(t *testing.T, c opClient.Interface, name string, checker csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	var fetched *v1alpha1.ClusterServiceVersion
	var err error

	unstructuredConverter := conversion.NewConverter(true)
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, v1alpha1.GroupVersion, testNamespace, v1alpha1.ClusterServiceVersionKind, name)
		if err != nil {
			return false, err
		}

		err = unstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, &fetched)
		require.NoError(t, err)
		t.Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return checker(fetched), nil
	})

	return fetched, err
}

func waitForDeploymentToDelete(t *testing.T, c opClient.Interface, name string) error {
	return wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := c.GetDeployment(testNamespace, name)
		if errors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})
}

func waitForCSVToDelete(t *testing.T, c opClient.Interface, name string) (*v1alpha1.ClusterServiceVersion, error) {
	var fetched *v1alpha1.ClusterServiceVersion
	var err error

	unstructuredConverter := conversion.NewConverter(true)
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, v1alpha1.GroupVersion, testNamespace, v1alpha1.ClusterServiceVersionKind, name)
		if errors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if err := unstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, &fetched); err == nil {
			t.Logf("%s still exists", fetched.Name)
		}
		return false, nil
	})

	return fetched, err
}

// TODO: same test but missing serviceaccount instead
func TestCreateCSVWithUnmetRequirements(t *testing.T) {
	c := newKubeClient(t)

	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep"),
				Spec: newNginxDeployment(),
			},
		},
	}
	strategyRaw, err := json.Marshal(strategy)
	require.NoError(t, err)

	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName:    install.InstallStrategyNameDeployment,
				StrategySpecRaw: strategyRaw,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:    "not.in.cluster.com",
						Version: "v1alpha1",
						Kind:    "NotInCluster",
					},
				},
			},
		},
	}

	cleanupCSV, err := createCSV(c, csv)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, c, csv.Name, csvPendingChecker)
	require.NoError(t, err)

	// Shouldn't create deployment
	_, err = c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.Error(t, err)
}

// TODO: same test but create serviceaccount instead
func TestCreateCSVRequirementsMet(t *testing.T) {
	c := newKubeClient(t)

	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep"),
				Spec: newNginxDeployment(),
			},
		},
	}
	strategyRaw, err := json.Marshal(strategy)
	require.NoError(t, err)

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"

	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "csv1",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName:    install.InstallStrategyNameDeployment,
				StrategySpecRaw: strategyRaw,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:    crdName,
						Version: "v1alpha1",
						Kind:    crdPlural,
					},
				},
			},
		},
	}

	// Create dependency first (CRD)
	cleanupCRD, err := createCRD(c, extv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: extv1beta1.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: extv1beta1.CustomResourceDefinitionNames{
				Plural:   crdPlural,
				Singular: crdPlural,
				Kind:     crdPlural,
				ListKind: "list" + crdPlural,
			},
			Scope: "Namespaced",
		},
	})
	require.NoError(t, err)
	defer cleanupCRD()

	cleanupCSV, err := createCSV(c, csv)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, c, csv.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Should create deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.Equal(t, strategy.DeploymentSpecs[0].Name, dep.Name)
}

func TestUpdateCSVSameDeploymentName(t *testing.T) {
	c := newKubeClient(t)

	// create "current" CSV
	strategy := install.StrategyDetailsDeployment{
		Permissions: []install.StrategyDeploymentPermissions{
			{
				ServiceAccountName: "csv-sa",
				Rules: []rbacv1beta1.PolicyRule{
					{
						Verbs:     []string{"list", "delete"},
						APIGroups: []string{""},
						Resources: []string{"pods"},
					},
				},
			},
			{
				ServiceAccountName: "old-csv-sa",
				Rules: []rbacv1beta1.PolicyRule{
					{
						Verbs:     []string{"list", "delete"},
						APIGroups: []string{""},
						Resources: []string{"pods"},
					},
				},
			},
		},
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: "dep1",
				Spec: newNginxDeployment(),
			},
		},
	}
	strategyRaw, err := json.Marshal(strategy)
	require.NoError(t, err)

	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "csv1",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName:    install.InstallStrategyNameDeployment,
				StrategySpecRaw: strategyRaw,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:    "ins.cluster.com",
						Version: "v1alpha1",
						Kind:    "InCluster",
					},
				},
			},
		},
	}

	// Create dependency first (CRD)
	cleanupCRD, err := createCRD(c, extv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ins.cluster.com",
		},
		Spec: extv1beta1.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: extv1beta1.CustomResourceDefinitionNames{
				Plural:   "ins",
				Singular: "in",
				Kind:     "InCluster",
				ListKind: "InClusterList",
			},
			Scope: "Namespaced",
		},
	})
	require.NoError(t, err)
	defer cleanupCRD()

	cleanupCSV, err := createCSV(c, csv)
	require.NoError(t, err)
	defer cleanupCSV()

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, c, csv.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Create "updated" CSV
	strategyNew := install.StrategyDetailsDeployment{
		Permissions: []install.StrategyDeploymentPermissions{
			{
				ServiceAccountName: "csv-sa",
				Rules: []rbacv1beta1.PolicyRule{
					{
						Verbs:     []string{"list", "delete"},
						APIGroups: []string{""},
						Resources: []string{"pods"},
					},
				},
			},
			{
				ServiceAccountName: "new-csv-sa",
				Rules: []rbacv1beta1.PolicyRule{
					{
						Verbs:     []string{"list", "delete"},
						APIGroups: []string{""},
						Resources: []string{"pods"},
					},
				},
			},
		},
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				// Same name
				Name: "dep1",
				// Different spec
				Spec: newNginxDeployment(),
			},
		},
	}
	strategyNewRaw, err := json.Marshal(strategyNew)
	require.NoError(t, err)

	csvNew := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "csv2",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces: csv.Name,
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName:    install.InstallStrategyNameDeployment,
				StrategySpecRaw: strategyNewRaw,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:    "ins.cluster.com",
						Version: "v1alpha1",
						Kind:    "InCluster",
					},
				},
			},
		},
	}

	cleanupNewCSV, err := createCSV(c, csvNew)
	require.NoError(t, err)
	defer cleanupNewCSV()

	// Wait for updated CSV to succeed
	_, err = fetchCSV(t, c, csvNew.Name, csvSucceededChecker)
	require.NoError(t, err)

	// should have csv-sa and old-csv-sa
	_, err = c.GetServiceAccount(testNamespace, "csv-sa")
	require.NoError(t, err)
	_, err = c.GetServiceAccount(testNamespace, "old-csv-sa")
	require.NoError(t, err)

	// Should have updated existing deployment
	depUpdated, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depUpdated)
	require.Equal(t, depUpdated.Spec.Template.Spec.Containers[0].Name, strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name)

	// should have csv-sa and old-csv-sa
	_, err = c.GetServiceAccount(testNamespace, "csv-sa")
	require.NoError(t, err)
	_, err = c.GetServiceAccount(testNamespace, "new-csv-sa")
	require.NoError(t, err)

	// Should eventually GC the CSV
	_, err = waitForCSVToDelete(t, c, csv.Name)
	require.NoError(t, err)

	// csv-sa shouldn't have been GC'd
	_, err = c.GetServiceAccount(testNamespace, "csv-sa")
	require.NoError(t, err)
}

func TestUpdateCSVDifferentDeploymentName(t *testing.T) {
	c := newKubeClient(t)

	// create "current" CSV
	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: "dep1",
				Spec: newNginxDeployment(),
			},
		},
	}
	strategyRaw, err := json.Marshal(strategy)
	require.NoError(t, err)

	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "csv1",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName:    install.InstallStrategyNameDeployment,
				StrategySpecRaw: strategyRaw,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:    "ins2.cluster.com",
						Version: "v1alpha1",
						Kind:    "InCluster2",
					},
				},
			},
		},
	}

	// Create dependency first (CRD)
	cleanupCRD, err := createCRD(c, extv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ins2.cluster.com",
		},
		Spec: extv1beta1.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: extv1beta1.CustomResourceDefinitionNames{
				Plural:   "ins2",
				Singular: "in2",
				Kind:     "InCluster2",
				ListKind: "InClusterList2",
			},
			Scope: "Namespaced",
		},
	})
	require.NoError(t, err)
	defer cleanupCRD()

	cleanupCSV, err := createCSV(c, csv)
	require.NoError(t, err)
	defer cleanupCSV()

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, c, csv.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Create "updated" CSV
	strategyNew := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: "dep2",
				Spec: newNginxDeployment(),
			},
		},
	}
	strategyNewRaw, err := json.Marshal(strategyNew)
	require.NoError(t, err)

	csvNew := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "csv2",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces: csv.Name,
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName:    install.InstallStrategyNameDeployment,
				StrategySpecRaw: strategyNewRaw,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:    "ins2.cluster.com",
						Version: "v1alpha1",
						Kind:    "InCluster2",
					},
				},
			},
		},
	}

	cleanupNewCSV, err := createCSV(c, csvNew)
	require.NoError(t, err)
	defer cleanupNewCSV()

	// Wait for updated CSV to succeed
	_, err = fetchCSV(t, c, csvNew.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created new deployment and deleted old
	depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depNew)
	err = waitForDeploymentToDelete(t, c, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)

	// Should eventually GC the CSV
	_, err = waitForCSVToDelete(t, c, csv.Name)
	require.NoError(t, err)
}

// TODO: test behavior when replaces field doesn't point to existing CSV
