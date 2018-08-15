package e2e

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

var singleInstance = int32(1)

type cleanupFunc func()

var immediateDeleteGracePeriod int64 = 0

func buildCSVCleanupFunc(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, deleteCRDs bool) cleanupFunc {
	return func() {
		require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(csv.GetName(), &metav1.DeleteOptions{}))
		if deleteCRDs {
			for _, crd := range csv.Spec.CustomResourceDefinitions.Owned {
				buildCRDCleanupFunc(c, crd.Name)()
			}
		}

		require.NoError(t, waitForDelete(func() error {
			_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(csv.GetName(), metav1.GetOptions{})
			return err
		}))
	}
}

func createCSV(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, cleanupCRDs bool) (cleanupFunc, error) {
	csv.Kind = v1alpha1.ClusterServiceVersionKind
	csv.APIVersion = v1alpha1.SchemeGroupVersion.String()
	_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Create(&csv)
	require.NoError(t, err)
	return buildCSVCleanupFunc(t, c, crc, csv, namespace, cleanupCRDs), nil

}

func buildCRDCleanupFunc(c operatorclient.ClientInterface, crdName string) cleanupFunc {
	return func() {
		err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Delete(crdName, &metav1.DeleteOptions{GracePeriodSeconds: &immediateDeleteGracePeriod})
		if err != nil {
			fmt.Println(err)
		}

		waitForDelete(func() error {
			_, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(crdName, metav1.GetOptions{})
			return err
		})
	}
}

func createCRD(c operatorclient.ClientInterface, crd extv1beta1.CustomResourceDefinition) (cleanupFunc, error) {
	_, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Create(&crd)
	if err != nil {
		return nil, err
	}

	return buildCRDCleanupFunc(c, crd.GetName()), nil
}

func newNginxDeployment(name string) appsv1.DeploymentSpec {
	return appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": name,
			},
		},
		Replicas: &singleInstance,
		Template: v1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": name,
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

var csvAnyChecker = func(csv *v1alpha1.ClusterServiceVersion) bool {
	return csvPendingChecker(csv) || csvSucceededChecker(csv) || csvReplacingChecker(csv)
}

func fetchCSV(t *testing.T, c versioned.Interface, name string, checker csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	var fetched *v1alpha1.ClusterServiceVersion
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err = c.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		t.Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return checker(fetched), nil
	})

	return fetched, err
}

func waitForDeploymentToDelete(t *testing.T, c operatorclient.ClientInterface, name string) error {
	return wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		t.Logf("waiting for deployment %s to delete", name)
		_, err := c.GetDeployment(testNamespace, name)
		if errors.IsNotFound(err) {
			t.Logf("deleted %s", name)
			return true, nil
		}
		if err != nil {
			t.Logf("err trying to delete %s: %s", name, err)
			return false, err
		}
		return false, nil
	})
}

func waitForCSVToDelete(t *testing.T, c versioned.Interface, name string) error {
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err := c.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		t.Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		if err != nil {
			return false, err
		}
		return false, nil
	})

	return err
}

// TODO: same test but missing serviceaccount instead
func TestCreateCSVWithUnmetRequirements(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
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
						DisplayName: "Not In Cluster",
						Description: "A CRD that is not currently in the cluster",
						Name:        "not.in.cluster.com",
						Version:     "v1alpha1",
						Kind:        "NotInCluster",
					},
				},
			},
		},
	}

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csv.Name, csvPendingChecker)
	require.NoError(t, err)

	// Shouldn't create deployment
	_, err = c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.Error(t, err)
}

// TODO: same test but create serviceaccount instead
func TestCreateCSVRequirementsMet(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
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
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: crdName,
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

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, true)
	require.NoError(t, err)
	defer cleanupCSV()

	fetchedCSV, err := fetchCSV(t, crc, csv.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Should create deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.Equal(t, strategy.DeploymentSpecs[0].Name, dep.Name)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err := fetchCSV(t, crc, csv.Name, csvSucceededChecker)
	require.NoError(t, err)
	compareResources(t, fetchedCSV, sameCSV)
}

func TestUpdateCSVSameDeploymentName(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create dependency first (CRD)
	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"
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

	// create "current" CSV
	nginxName := genName("nginx-")
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
				Name: genName("dep-"),
				Spec: newNginxDeployment(nginxName),
			},
		},
	}
	strategyRaw, err := json.Marshal(strategy)
	require.NoError(t, err)

	require.NoError(t, err)
	defer cleanupCRD()
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
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster",
					},
				},
			},
		},
	}

	// don't need to cleanup this CSV, it will be deleted by the upgrade process
	_, err = createCSV(t, c, crc, csv, testNamespace, true)
	require.NoError(t, err)

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// should have csv-sa and old-csv-sa
	_, err = c.GetServiceAccount(testNamespace, "csv-sa")
	require.NoError(t, err)
	_, err = c.GetServiceAccount(testNamespace, "old-csv-sa")
	require.NoError(t, err)

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
				Name: strategy.DeploymentSpecs[0].Name,
				// Different spec
				Spec: newNginxDeployment(nginxName),
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
			Name: genName("csv"),
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
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster",
					},
				},
			},
		},
	}

	cleanupNewCSV, err := createCSV(t, c, crc, csvNew, testNamespace, true)
	require.NoError(t, err)
	defer cleanupNewCSV()

	// Wait for updated CSV to succeed
	fetchedCSV, err := fetchCSV(t, crc, csvNew.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Should have updated existing deployment
	depUpdated, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depUpdated)
	require.Equal(t, depUpdated.Spec.Template.Spec.Containers[0].Name, strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name)

	// should have csv-sa and new-csv-sa
	_, err = c.GetServiceAccount(testNamespace, "csv-sa")
	require.NoError(t, err)
	_, err = c.GetServiceAccount(testNamespace, "new-csv-sa")
	require.NoError(t, err)

	// Should eventually GC the CSV
	err = waitForCSVToDelete(t, crc, csv.Name)
	require.NoError(t, err)

	// csv-sa shouldn't have been GC'd
	_, err = c.GetServiceAccount(testNamespace, "csv-sa")
	require.NoError(t, err)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err := fetchCSV(t, crc, csvNew.Name, csvSucceededChecker)
	require.NoError(t, err)
	compareResources(t, fetchedCSV, sameCSV)
}

func TestUpdateCSVDifferentDeploymentName(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create dependency first (CRD)
	crdPlural := genName("ins2")
	crdName := crdPlural + ".cluster.com"
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

	// create "current" CSV
	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
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
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster2",
					},
				},
			},
		},
	}

	// don't need to clean up this CSV, it will be deleted by the upgrade process
	_, err = createCSV(t, c, crc, csv, testNamespace, true)
	require.NoError(t, err)

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Create "updated" CSV
	strategyNew := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep2"),
				Spec: newNginxDeployment(genName("nginx-")),
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
			Name: genName("csv2"),
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
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster2",
					},
				},
			},
		},
	}

	cleanupNewCSV, err := createCSV(t, c, crc, csvNew, testNamespace, true)
	require.NoError(t, err)
	defer cleanupNewCSV()

	// Wait for updated CSV to succeed
	fetchedCSV, err := fetchCSV(t, crc, csvNew.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err := fetchCSV(t, crc, csvNew.Name, csvSucceededChecker)
	require.NoError(t, err)
	compareResources(t, fetchedCSV, sameCSV)

	// Should have created new deployment and deleted old
	depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depNew)
	err = waitForDeploymentToDelete(t, c, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)

	// Should eventually GC the CSV
	err = waitForCSVToDelete(t, crc, csv.Name)
	require.NoError(t, err)
}

func TestUpdateCSVMultipleIntermediates(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create dependency first (CRD)
	crdPlural := genName("ins3")
	crdName := crdPlural + ".cluster.com"
	cleanupCRD, err := createCRD(c, extv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: extv1beta1.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Versions: []extv1beta1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
				},
			},
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

	// create "current" CSV
	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
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
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster3",
					},
				},
			},
		},
	}

	// don't need to clean up this CSV, it will be deleted by the upgrade process
	_, err = createCSV(t, c, crc, csv, testNamespace, true)
	require.NoError(t, err)

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Create "updated" CSV
	strategyNew := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep2"),
				Spec: newNginxDeployment(genName("nginx-")),
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
			Name: genName("csv2"),
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
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster3",
					},
				},
			},
		},
	}

	cleanupNewCSV, err := createCSV(t, c, crc, csvNew, testNamespace, true)
	require.NoError(t, err)
	defer cleanupNewCSV()

	// Wait for updated CSV to succeed
	fetchedCSV, err := fetchCSV(t, crc, csvNew.Name, csvSucceededChecker)
	require.NoError(t, err)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err := fetchCSV(t, crc, csvNew.Name, csvSucceededChecker)
	require.NoError(t, err)
	compareResources(t, fetchedCSV, sameCSV)

	// Should have created new deployment and deleted old
	depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depNew)
	err = waitForDeploymentToDelete(t, c, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)

	// Should eventually GC the CSV
	err = waitForCSVToDelete(t, crc, csv.Name)
	require.NoError(t, err)
}

// TODO: test behavior when replaces field doesn't point to existing CSV
