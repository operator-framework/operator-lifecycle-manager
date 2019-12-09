package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

var singleInstance = int32(1)

type cleanupFunc func()

var immediateDeleteGracePeriod int64 = 0

func findLastEvent(events *corev1.EventList) (event corev1.Event) {
	var latestTime metav1.Time
	var latestInd int
	for i, item := range events.Items {
		if i != 0 {
			if latestTime.Before(&item.LastTimestamp) {
				latestTime = item.LastTimestamp
				latestInd = i
			}
		} else {
			latestTime = item.LastTimestamp
		}
	}
	return events.Items[latestInd]
}

func buildCSVCleanupFunc(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, deleteCRDs, deleteAPIServices bool) cleanupFunc {
	return func() {
		require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(csv.GetName(), &metav1.DeleteOptions{}))
		if deleteCRDs {
			for _, crd := range csv.Spec.CustomResourceDefinitions.Owned {
				buildCRDCleanupFunc(c, crd.Name)()
			}
		}

		if deleteAPIServices {
			for _, desc := range csv.GetOwnedAPIServiceDescriptions() {
				buildAPIServiceCleanupFunc(c, desc.Name)()
			}
		}

		require.NoError(t, waitForDelete(func() error {
			_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(csv.GetName(), metav1.GetOptions{})
			return err
		}))
	}
}

func createCSV(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface, csv v1alpha1.ClusterServiceVersion, namespace string, cleanupCRDs, cleanupAPIServices bool) (cleanupFunc, error) {
	csv.Kind = v1alpha1.ClusterServiceVersionKind
	csv.APIVersion = v1alpha1.SchemeGroupVersion.String()
	_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Create(&csv)
	require.NoError(t, err)
	return buildCSVCleanupFunc(t, c, crc, csv, namespace, cleanupCRDs, cleanupAPIServices), nil

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

func buildAPIServiceCleanupFunc(c operatorclient.ClientInterface, apiServiceName string) cleanupFunc {
	return func() {
		err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Delete(apiServiceName, &metav1.DeleteOptions{GracePeriodSeconds: &immediateDeleteGracePeriod})
		if err != nil {
			fmt.Println(err)
		}

		waitForDelete(func() error {
			_, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Get(apiServiceName, metav1.GetOptions{})
			return err
		})
	}
}

func createCRD(c operatorclient.ClientInterface, crd apiextensions.CustomResourceDefinition) (cleanupFunc, error) {
	out := &v1beta1.CustomResourceDefinition{}
	scheme := runtime.NewScheme()
	if err := apiextensions.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := v1beta1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := scheme.Convert(&crd, out, nil); err != nil {
		return nil, err
	}
	_, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Create(out)
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
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": name,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  genName("nginx"),
						Image: *dummyImage,
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 80,
							},
						},
						ImagePullPolicy: corev1.PullIfNotPresent,
					},
				},
			},
		},
	}
}

func newMockExtServerDeployment(name, mockGroupVersion string, mockKinds []string) appsv1.DeploymentSpec {
	return appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": name,
			},
		},
		Replicas: &singleInstance,
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": name,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    genName(name),
						Image:   "quay.io/coreos/mock-extension-apiserver:master",
						Command: []string{"/bin/mock-extension-apiserver"},
						Args: []string{
							"-v=4",
							"--mock-kinds",
							strings.Join(mockKinds, ","),
							"--mock-group-version",
							mockGroupVersion,
							"--secure-port",
							"5443",
							"--debug",
						},
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 5443,
							},
						},
						ImagePullPolicy: corev1.PullIfNotPresent,
					},
				},
			},
		},
	}
}

type csvConditionChecker func(csv *v1alpha1.ClusterServiceVersion) bool

func buildCSVConditionChecker(phases ...v1alpha1.ClusterServiceVersionPhase) csvConditionChecker {
	return func(csv *v1alpha1.ClusterServiceVersion) bool {
		conditionMet := false
		for _, phase := range phases {
			conditionMet = conditionMet || csv.Status.Phase == phase
		}
		return conditionMet
	}
}

func buildCSVReasonChecker(reasons ...v1alpha1.ConditionReason) csvConditionChecker {
	return func(csv *v1alpha1.ClusterServiceVersion) bool {
		conditionMet := false
		for _, reason := range reasons {
			conditionMet = conditionMet || csv.Status.Reason == reason
		}
		return conditionMet
	}
}

var csvPendingChecker = buildCSVConditionChecker(v1alpha1.CSVPhasePending)
var csvSucceededChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded)
var csvReplacingChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseReplacing, v1alpha1.CSVPhaseDeleting)
var csvFailedChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseFailed)
var csvAnyChecker = buildCSVConditionChecker(v1alpha1.CSVPhasePending, v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVPhaseReplacing, v1alpha1.CSVPhaseDeleting, v1alpha1.CSVPhaseFailed)
var csvCopiedChecker = buildCSVReasonChecker(v1alpha1.CSVReasonCopied)

func fetchCSV(t *testing.T, c versioned.Interface, name, namespace string, checker csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	var fetched *v1alpha1.ClusterServiceVersion
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err = c.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		t.Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return checker(fetched), nil
	})

	if err != nil {
		t.Logf("never got correct status: %#v", fetched.Status)
	}
	return fetched, err
}

func awaitCSV(t *testing.T, c versioned.Interface, namespace, name string, checker csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	var fetched *v1alpha1.ClusterServiceVersion
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err = c.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(name, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		t.Logf("%s - %s (%s): %s", name, fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return checker(fetched), nil
	})

	if err != nil {
		t.Logf("never got correct status: %#v", fetched.Status)
	}
	return fetched, err
}

func waitForDeployment(t *testing.T, c operatorclient.ClientInterface, name string) error {
	return wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := c.GetDeployment(testNamespace, name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
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

func TestCreateCSVWithUnmetRequirementsMinKubeVersion(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	depName := genName("dep-")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "999.999.999",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
		},
	}

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvPendingChecker)
	require.NoError(t, err)

	// Shouldn't create deployment
	_, err = c.GetDeployment(testNamespace, depName)
	require.Error(t, err)
}

// TODO: same test but missing serviceaccount instead
func TestCreateCSVWithUnmetRequirementsCRD(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	depName := genName("dep-")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
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

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvPendingChecker)
	require.NoError(t, err)

	// Shouldn't create deployment
	_, err = c.GetDeployment(testNamespace, depName)
	require.Error(t, err)
}

func TestCreateCSVWithUnmetPermissionsCRD(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	saName := genName("dep-")
	permissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: saName,
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		},
	}

	clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: saName,
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		},
	}

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"
	depName := genName("dep-")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
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
	cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
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

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, true, false)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvPendingChecker)
	require.NoError(t, err)

	// Shouldn't create deployment
	_, err = c.GetDeployment(testNamespace, depName)
	require.Error(t, err)
}

func TestCreateCSVWithUnmetRequirementsAPIService(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	depName := genName("dep-")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Required: []v1alpha1.APIServiceDescription{
					{
						DisplayName: "Not In Cluster",
						Description: "An apiservice that is not currently in the cluster",
						Group:       "not.in.cluster.com",
						Version:     "v1alpha1",
						Kind:        "NotInCluster",
					},
				},
			},
		},
	}

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvPendingChecker)
	require.NoError(t, err)

	// Shouldn't create deployment
	_, err = c.GetDeployment(testNamespace, depName)
	require.Error(t, err)
}

func TestCreateCSVWithUnmetPermissionsAPIService(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	saName := genName("dep-")
	permissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: saName,
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		},
	}

	clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: saName,
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		},
	}

	depName := genName("dep-")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
			// Cheating a little; this is an APIservice that will exist for the e2e tests
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Required: []v1alpha1.APIServiceDescription{
					{
						Group:       "packages.operators.coreos.com",
						Version:     "v1",
						Kind:        "PackageManifest",
						DisplayName: "Package Manifest",
						Description: "An apiservice that exists",
					},
				},
			},
		},
	}

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvPendingChecker)
	require.NoError(t, err)

	// Shouldn't create deployment
	_, err = c.GetDeployment(testNamespace, depName)
	require.Error(t, err)
}

func TestCreateCSVWithUnmetRequirementsNativeAPI(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	depName := genName("dep-")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
			NativeAPIs:      []metav1.GroupVersionKind{{Group: "kubenative.io", Version: "v1", Kind: "Native"}},
		},
	}

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvPendingChecker)
	require.NoError(t, err)

	// Shouldn't create deployment
	_, err = c.GetDeployment(testNamespace, depName)
	require.Error(t, err)
}

// TODO: same test but create serviceaccount instead
func TestCreateCSVRequirementsMetCRD(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	sa := corev1.ServiceAccount{}
	sa.SetName(genName("sa-"))
	sa.SetNamespace(testNamespace)
	_, err := c.CreateServiceAccount(&sa)
	require.NoError(t, err, "could not create ServiceAccount %#v", sa)

	permissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: sa.GetName(),
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		},
	}

	clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: sa.GetName(),
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
				{
					Verbs:           []string{"put", "post", "get"},
					NonResourceURLs: []string{"/osb", "/osb/*"},
				},
			},
		},
	}

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"
	depName := genName("dep-")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
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

	// Create CSV first, knowing it will fail
	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, true, false)
	require.NoError(t, err)
	defer cleanupCSV()

	fetchedCSV, err := fetchCSV(t, crc, csv.Name, testNamespace, csvPendingChecker)
	require.NoError(t, err)

	crd := apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
				Plural:   crdPlural,
				Singular: crdPlural,
				Kind:     crdPlural,
				ListKind: "list" + crdPlural,
			},
			Scope: "Namespaced",
		},
	}
	crd.SetOwnerReferences([]metav1.OwnerReference{{
		Name:       fetchedCSV.GetName(),
		APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		Kind:       v1alpha1.ClusterServiceVersionKind,
		UID:        fetchedCSV.GetUID(),
	}})
	cleanupCRD, err := createCRD(c, crd)
	defer cleanupCRD()
	require.NoError(t, err)

	// Create Role/Cluster Roles and RoleBindings
	role := rbacv1.Role{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"create"},
				APIGroups: []string{""},
				Resources: []string{"deployment"},
			},
		},
	}
	role.SetName(genName("dep-"))
	role.SetNamespace(testNamespace)
	_, err = c.CreateRole(&role)
	require.NoError(t, err, "could not create Role")

	roleBinding := rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     role.GetName(),
		},
	}
	roleBinding.SetName(genName("dep-"))
	roleBinding.SetNamespace(testNamespace)
	_, err = c.CreateRoleBinding(&roleBinding)
	require.NoError(t, err, "could not create RoleBinding")

	clusterRole := rbacv1.ClusterRole{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get"},
				APIGroups: []string{""},
				Resources: []string{"deployment"},
			},
		},
	}
	clusterRole.SetName(genName("dep-"))
	_, err = c.CreateClusterRole(&clusterRole)
	require.NoError(t, err, "could not create ClusterRole")

	nonResourceClusterRole := rbacv1.ClusterRole{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:           []string{"put", "post", "get"},
				NonResourceURLs: []string{"/osb", "/osb/*"},
			},
		},
	}
	nonResourceClusterRole.SetName(genName("dep-"))
	_, err = c.CreateClusterRole(&nonResourceClusterRole)
	require.NoError(t, err, "could not create ClusterRole")

	clusterRoleBinding := rbacv1.ClusterRoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     clusterRole.GetName(),
		},
	}
	clusterRoleBinding.SetName(genName("dep-"))
	_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
	require.NoError(t, err, "could not create ClusterRoleBinding")

	nonResourceClusterRoleBinding := rbacv1.ClusterRoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     nonResourceClusterRole.GetName(),
		},
	}
	nonResourceClusterRoleBinding.SetName(genName("dep-"))
	_, err = c.CreateClusterRoleBinding(&nonResourceClusterRoleBinding)
	require.NoError(t, err, "could not create ClusterRoleBinding")

	fmt.Println("checking for deployment")
	// Poll for deployment to be ready
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		dep, err := c.GetDeployment(testNamespace, depName)
		if k8serrors.IsNotFound(err) {
			fmt.Printf("deployment %s not found\n", depName)
			return false, nil
		} else if err != nil {
			fmt.Printf("unexpected error fetching deployment %s\n", depName)
			return false, err
		}
		if dep.Status.UpdatedReplicas == *(dep.Spec.Replicas) &&
			dep.Status.Replicas == *(dep.Spec.Replicas) &&
			dep.Status.AvailableReplicas == *(dep.Spec.Replicas) {
			fmt.Println("deployment ready")
			return true, nil
		}
		fmt.Println("deployment not ready")
		return false, nil
	})
	require.NoError(t, err)

	fetchedCSV, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Delete CRD
	cleanupCRD()

	// Wait for CSV failure
	fetchedCSV, err = fetchCSV(t, crc, csv.Name, testNamespace, csvPendingChecker)
	require.NoError(t, err)

	// Recreate the CRD
	cleanupCRD, err = createCRD(c, crd)
	require.NoError(t, err)
	defer cleanupCRD()

	// Wait for CSV success again
	fetchedCSV, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)
}

func TestCreateCSVRequirementsMetAPIService(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	sa := corev1.ServiceAccount{}
	sa.SetName(genName("sa-"))
	sa.SetNamespace(testNamespace)
	_, err := c.CreateServiceAccount(&sa)
	require.NoError(t, err, "could not create ServiceAccount")

	permissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: sa.GetName(),
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		},
	}

	clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: sa.GetName(),
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		},
	}

	depName := genName("dep-")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
			// Cheating a little; this is an APIservice that will exist for the e2e tests
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Required: []v1alpha1.APIServiceDescription{
					{
						Group:       "packages.operators.coreos.com",
						Version:     "v1",
						Kind:        "PackageManifest",
						DisplayName: "Package Manifest",
						Description: "An apiservice that exists",
					},
				},
			},
		},
	}

	// Create Role/Cluster Roles and RoleBindings
	role := rbacv1.Role{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"create"},
				APIGroups: []string{""},
				Resources: []string{"deployment"},
			},
		},
	}
	role.SetName(genName("dep-"))
	role.SetNamespace(testNamespace)
	_, err = c.CreateRole(&role)
	require.NoError(t, err, "could not create Role")

	roleBinding := rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     role.GetName(),
		},
	}
	roleBinding.SetName(genName("dep-"))
	roleBinding.SetNamespace(testNamespace)
	_, err = c.CreateRoleBinding(&roleBinding)
	require.NoError(t, err, "could not create RoleBinding")

	clusterRole := rbacv1.ClusterRole{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get"},
				APIGroups: []string{""},
				Resources: []string{"deployment"},
			},
		},
	}
	clusterRole.SetName(genName("dep-"))
	_, err = c.CreateClusterRole(&clusterRole)
	require.NoError(t, err, "could not create ClusterRole")

	clusterRoleBinding := rbacv1.ClusterRoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     clusterRole.GetName(),
		},
	}
	clusterRoleBinding.SetName(genName("dep-"))
	_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
	require.NoError(t, err, "could not create ClusterRoleBinding")

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)
	defer cleanupCSV()

	fetchedCSV, err := fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err := fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)
	compareResources(t, fetchedCSV, sameCSV)
}

func TestCreateCSVWithOwnedAPIService(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	depName := genName("hat-server")
	mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
	version := "v1alpha1"
	mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
	mockKinds := []string{"fez", "fedora"}
	depSpec := newMockExtServerDeployment(depName, mockGroupVersion, mockKinds)
	apiServiceName := strings.Join([]string{version, mockGroup}, ".")

	// Create CSV for the package-server
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: depName,
				Spec: depSpec,
			},
		},
	}

	owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
	for i, kind := range mockKinds {
		owned[i] = v1alpha1.APIServiceDescription{
			Name:           apiServiceName,
			Group:          mockGroup,
			Version:        version,
			Kind:           kind,
			DeploymentName: depName,
			ContainerPort:  int32(5443),
			DisplayName:    kind,
			Description:    fmt.Sprintf("A %s", kind),
		}
	}

	csv := v1alpha1.ClusterServiceVersion{
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned: owned,
			},
		},
	}
	csv.SetName(depName)

	// Create the APIService CSV
	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)
	defer func() {
		watcher, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Watch(metav1.ListOptions{FieldSelector: "metadata.name=" + apiServiceName})
		require.NoError(t, err)

		deleted := make(chan struct{})
		go func() {
			events := watcher.ResultChan()
			for {
				select {
				case evt := <-events:
					if evt.Type == watch.Deleted {
						deleted <- struct{}{}
						return
					}
				case <-time.After(pollDuration):
					require.FailNow(t, "apiservice not cleaned up after CSV deleted")
				}
			}
		}()

		cleanupCSV()
		<-deleted
	}()

	fetchedCSV, err := fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should create Deployment
	dep, err := c.GetDeployment(testNamespace, depName)
	require.NoError(t, err, "error getting expected Deployment")

	// Should create APIService
	apiService, err := c.GetAPIService(apiServiceName)
	require.NoError(t, err, "error getting expected APIService")

	// Should create Service
	_, err = c.GetService(testNamespace, olm.APIServiceNameToServiceName(apiServiceName))
	require.NoError(t, err, "error getting expected Service")

	// Should create certificate Secret
	secretName := fmt.Sprintf("%s-cert", apiServiceName)
	_, err = c.GetSecret(testNamespace, secretName)
	require.NoError(t, err, "error getting expected Secret")

	// Should create a Role for the Secret
	_, err = c.GetRole(testNamespace, secretName)
	require.NoError(t, err, "error getting expected Secret Role")

	// Should create a RoleBinding for the Secret
	_, err = c.GetRoleBinding(testNamespace, secretName)
	require.NoError(t, err, "error getting exptected Secret RoleBinding")

	// Should create a system:auth-delegator Cluster RoleBinding
	_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", apiServiceName))
	require.NoError(t, err, "error getting expected system:auth-delegator ClusterRoleBinding")

	// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
	_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", apiServiceName))
	require.NoError(t, err, "error getting expected extension-apiserver-authentication-reader RoleBinding")

	// Store the ca sha annotation
	oldCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[olm.OLMCAHashAnnotationKey]
	require.True(t, ok, "expected olm sha annotation not present on existing pod template")

	// Induce a cert rotation
	now := metav1.Now()
	fetchedCSV.Status.CertsLastUpdated = &now
	fetchedCSV.Status.CertsRotateAt = &now
	fetchedCSV, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).UpdateStatus(fetchedCSV)
	require.NoError(t, err)

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, func(csv *v1alpha1.ClusterServiceVersion) bool {
		// Should create deployment
		dep, err = c.GetDeployment(testNamespace, depName)
		require.NoError(t, err)

		// Should have a new ca hash annotation
		newCAAnnotation, ok := dep.Spec.Template.GetAnnotations()[olm.OLMCAHashAnnotationKey]
		require.True(t, ok, "expected olm sha annotation not present in new pod template")

		if newCAAnnotation != oldCAAnnotation {
			// Check for success
			return csvSucceededChecker(csv)
		}

		return false
	})
	require.NoError(t, err, "failed to rotate cert")

	// Get the APIService UID
	oldAPIServiceUID := apiService.GetUID()

	// Delete the APIService
	err = c.DeleteAPIService(apiServiceName, &metav1.DeleteOptions{})
	require.NoError(t, err)

	// Wait for CSV success
	fetchedCSV, err = fetchCSV(t, crc, csv.GetName(), testNamespace, func(csv *v1alpha1.ClusterServiceVersion) bool {
		// Should create an APIService
		apiService, err := c.GetAPIService(apiServiceName)
		if err != nil {
			require.True(t, k8serrors.IsNotFound(err))
			return false
		}

		if csvSucceededChecker(csv) {
			require.NotEqual(t, oldAPIServiceUID, apiService.GetUID())
			return true
		}

		return false
	})
	require.NoError(t, err)
}

func TestUpdateCSVWithOwnedAPIService(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	depName := genName("hat-server")
	mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
	version := "v1alpha1"
	mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
	mockKinds := []string{"fedora"}
	depSpec := newMockExtServerDeployment(depName, mockGroupVersion, mockKinds)
	apiServiceName := strings.Join([]string{version, mockGroup}, ".")

	// Create CSVs for the hat-server
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: depName,
				Spec: depSpec,
			},
		},
	}

	owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
	for i, kind := range mockKinds {
		owned[i] = v1alpha1.APIServiceDescription{
			Name:           apiServiceName,
			Group:          mockGroup,
			Version:        version,
			Kind:           kind,
			DeploymentName: depName,
			ContainerPort:  int32(5443),
			DisplayName:    kind,
			Description:    fmt.Sprintf("A %s", kind),
		}
	}

	csv := v1alpha1.ClusterServiceVersion{
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned: owned,
			},
		},
	}
	csv.SetName("csv-hat-1")

	// Create the APIService CSV
	_, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should create Deployment
	_, err = c.GetDeployment(testNamespace, depName)
	require.NoError(t, err, "error getting expected Deployment")

	// Should create APIService
	_, err = c.GetAPIService(apiServiceName)
	require.NoError(t, err, "error getting expected APIService")

	// Should create Service
	_, err = c.GetService(testNamespace, olm.APIServiceNameToServiceName(apiServiceName))
	require.NoError(t, err, "error getting expected Service")

	// Should create certificate Secret
	secretName := fmt.Sprintf("%s-cert", apiServiceName)
	_, err = c.GetSecret(testNamespace, secretName)
	require.NoError(t, err, "error getting expected Secret")

	// Should create a Role for the Secret
	_, err = c.GetRole(testNamespace, secretName)
	require.NoError(t, err, "error getting expected Secret Role")

	// Should create a RoleBinding for the Secret
	_, err = c.GetRoleBinding(testNamespace, secretName)
	require.NoError(t, err, "error getting exptected Secret RoleBinding")

	// Should create a system:auth-delegator Cluster RoleBinding
	_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", apiServiceName))
	require.NoError(t, err, "error getting expected system:auth-delegator ClusterRoleBinding")

	// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
	_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", apiServiceName))
	require.NoError(t, err, "error getting expected extension-apiserver-authentication-reader RoleBinding")

	// Create a new CSV that owns the same API Service and replace the old CSV
	csv2 := v1alpha1.ClusterServiceVersion{
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:       csv.Name,
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned: owned,
			},
		},
	}
	csv2.SetName("csv-hat-2")

	// Create CSV2 to replace CSV
	cleanupCSV2, err := createCSV(t, c, crc, csv2, testNamespace, false, true)
	require.NoError(t, err)
	defer cleanupCSV2()

	_, err = fetchCSV(t, crc, csv2.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should create Deployment
	_, err = c.GetDeployment(testNamespace, depName)
	require.NoError(t, err, "error getting expected Deployment")

	// Should create APIService
	_, err = c.GetAPIService(apiServiceName)
	require.NoError(t, err, "error getting expected APIService")

	// Should create Service
	_, err = c.GetService(testNamespace, olm.APIServiceNameToServiceName(apiServiceName))
	require.NoError(t, err, "error getting expected Service")

	// Should create certificate Secret
	secretName = fmt.Sprintf("%s-cert", apiServiceName)
	_, err = c.GetSecret(testNamespace, secretName)
	require.NoError(t, err, "error getting expected Secret")

	// Should create a Role for the Secret
	_, err = c.GetRole(testNamespace, secretName)
	require.NoError(t, err, "error getting expected Secret Role")

	// Should create a RoleBinding for the Secret
	_, err = c.GetRoleBinding(testNamespace, secretName)
	require.NoError(t, err, "error getting exptected Secret RoleBinding")

	// Should create a system:auth-delegator Cluster RoleBinding
	_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", apiServiceName))
	require.NoError(t, err, "error getting expected system:auth-delegator ClusterRoleBinding")

	// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
	_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", apiServiceName))
	require.NoError(t, err, "error getting expected extension-apiserver-authentication-reader RoleBinding")

	// Should eventually GC the CSV
	err = waitForCSVToDelete(t, crc, csv.Name)
	require.NoError(t, err)

	// Rename the initial CSV
	csv.SetName("csv-hat-3")

	// Recreate the old CSV
	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, true)
	require.NoError(t, err)
	defer cleanupCSV()

	fetched, err := fetchCSV(t, crc, csv.Name, testNamespace, buildCSVReasonChecker(v1alpha1.CSVReasonOwnerConflict))
	require.NoError(t, err)
	require.Equal(t, string(v1alpha1.CSVPhaseFailed), string(fetched.Status.Phase))
}

func TestCreateSameCSVWithOwnedAPIServiceMultiNamespace(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create new namespace in a new operator group
	secondNamespaceName := genName(testNamespace + "-")
	matchingLabel := map[string]string{"inGroup": secondNamespaceName}

	_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   secondNamespaceName,
			Labels: matchingLabel,
		},
	})
	require.NoError(t, err)
	defer func() {
		err = c.KubernetesInterface().CoreV1().Namespaces().Delete(secondNamespaceName, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	// Create a new operator group for the new namespace
	operatorGroup := v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("e2e-operator-group-"),
			Namespace: secondNamespaceName,
		},
		Spec: v1.OperatorGroupSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: matchingLabel,
			},
		},
	}
	_, err = crc.OperatorsV1().OperatorGroups(secondNamespaceName).Create(&operatorGroup)
	require.NoError(t, err)
	defer func() {
		err = crc.OperatorsV1().OperatorGroups(secondNamespaceName).Delete(operatorGroup.Name, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	expectedOperatorGroupStatus := v1.OperatorGroupStatus{
		Namespaces: []string{secondNamespaceName},
	}

	t.Log("Waiting on new operator group to have correct status")
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, fetchErr := crc.OperatorsV1().OperatorGroups(secondNamespaceName).Get(operatorGroup.Name, metav1.GetOptions{})
		if fetchErr != nil {
			return false, fetchErr
		}
		if len(fetched.Status.Namespaces) > 0 {
			require.ElementsMatch(t, expectedOperatorGroupStatus.Namespaces, fetched.Status.Namespaces)
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	depName := genName("hat-server")
	mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
	version := "v1alpha1"
	mockGroupVersion := strings.Join([]string{mockGroup, version}, "/")
	mockKinds := []string{"fedora"}
	depSpec := newMockExtServerDeployment(depName, mockGroupVersion, mockKinds)
	apiServiceName := strings.Join([]string{version, mockGroup}, ".")

	// Create CSVs for the hat-server
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: depName,
				Spec: depSpec,
			},
		},
	}

	owned := make([]v1alpha1.APIServiceDescription, len(mockKinds))
	for i, kind := range mockKinds {
		owned[i] = v1alpha1.APIServiceDescription{
			Name:           apiServiceName,
			Group:          mockGroup,
			Version:        version,
			Kind:           kind,
			DeploymentName: depName,
			ContainerPort:  int32(5443),
			DisplayName:    kind,
			Description:    fmt.Sprintf("A %s", kind),
		}
	}

	csv := v1alpha1.ClusterServiceVersion{
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned: owned,
			},
		},
	}
	csv.SetName("csv-hat-1")

	// Create the initial CSV
	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should create Deployment
	_, err = c.GetDeployment(testNamespace, depName)
	require.NoError(t, err, "error getting expected Deployment")

	// Should create APIService
	_, err = c.GetAPIService(apiServiceName)
	require.NoError(t, err, "error getting expected APIService")

	// Should create Service
	_, err = c.GetService(testNamespace, olm.APIServiceNameToServiceName(apiServiceName))
	require.NoError(t, err, "error getting expected Service")

	// Should create certificate Secret
	secretName := fmt.Sprintf("%s-cert", apiServiceName)
	_, err = c.GetSecret(testNamespace, secretName)
	require.NoError(t, err, "error getting expected Secret")

	// Should create a Role for the Secret
	_, err = c.GetRole(testNamespace, secretName)
	require.NoError(t, err, "error getting expected Secret Role")

	// Should create a RoleBinding for the Secret
	_, err = c.GetRoleBinding(testNamespace, secretName)
	require.NoError(t, err, "error getting exptected Secret RoleBinding")

	// Should create a system:auth-delegator Cluster RoleBinding
	_, err = c.GetClusterRoleBinding(fmt.Sprintf("%s-system:auth-delegator", apiServiceName))
	require.NoError(t, err, "error getting expected system:auth-delegator ClusterRoleBinding")

	// Should create an extension-apiserver-authentication-reader RoleBinding in kube-system
	_, err = c.GetRoleBinding("kube-system", fmt.Sprintf("%s-auth-reader", apiServiceName))
	require.NoError(t, err, "error getting expected extension-apiserver-authentication-reader RoleBinding")

	// Create a new CSV that owns the same API Service but in a different namespace
	csv2 := v1alpha1.ClusterServiceVersion{
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned: owned,
			},
		},
	}
	csv2.SetName("csv-hat-2")

	// Create CSV2 to replace CSV
	_, err = createCSV(t, c, crc, csv2, secondNamespaceName, false, true)
	require.NoError(t, err)

	_, err = fetchCSV(t, crc, csv2.Name, secondNamespaceName, csvFailedChecker)
	require.NoError(t, err)
}

func TestOrphanedAPIServiceCleanUp(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)

	mockGroup := fmt.Sprintf("hats.%s.redhat.com", genName(""))
	version := "v1alpha1"
	apiServiceName := strings.Join([]string{version, mockGroup}, ".")

	apiService := &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Name: apiServiceName,
		},
		Spec: apiregistrationv1.APIServiceSpec{
			Group:                mockGroup,
			Version:              version,
			GroupPriorityMinimum: 100,
			VersionPriority:      100,
		},
	}

	watcher, err := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Watch(metav1.ListOptions{FieldSelector: "metadata.name=" + apiServiceName})
	require.NoError(t, err)

	deleted := make(chan struct{})
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		events := watcher.ResultChan()
		for {
			select {
			case <-quit:
				return
			case evt := <-events:
				if evt.Type == watch.Deleted {
					deleted <- struct{}{}
				}
			case <-time.After(pollDuration):
				require.FailNow(t, "orphaned apiservice not cleaned up as expected")
			}
		}
	}()

	_, err = c.CreateAPIService(apiService)
	require.NoError(t, err, "error creating expected APIService")
	orphanedAPISvc, err := c.GetAPIService(apiServiceName)
	require.NoError(t, err, "error getting expected APIService")

	newLabels := map[string]string{"olm.owner": "hat-serverfd4r5", "olm.owner.kind": "ClusterServiceVersion", "olm.owner.namespace": "nonexistent-namespace"}
	orphanedAPISvc.SetLabels(newLabels)
	_, err = c.UpdateAPIService(orphanedAPISvc)
	require.NoError(t, err, "error updating APIService")
	<-deleted

	_, err = c.CreateAPIService(apiService)
	require.NoError(t, err, "error creating expected APIService")
	orphanedAPISvc, err = c.GetAPIService(apiServiceName)
	require.NoError(t, err, "error getting expected APIService")

	newLabels = map[string]string{"olm.owner": "hat-serverfd4r5", "olm.owner.kind": "ClusterServiceVersion", "olm.owner.namespace": testNamespace}
	orphanedAPISvc.SetLabels(newLabels)
	_, err = c.UpdateAPIService(orphanedAPISvc)
	require.NoError(t, err, "error updating APIService")
	<-deleted
}

func TestUpdateCSVSameDeploymentName(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create dependency first (CRD)
	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"
	cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
				Plural:   crdPlural,
				Singular: crdPlural,
				Kind:     crdPlural,
				ListKind: "list" + crdPlural,
			},
			Scope: "Namespaced",
		},
	})

	// Create "current" CSV
	nginxName := genName("nginx-")
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(nginxName),
			},
		},
	}

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
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
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

	// Don't need to cleanup this CSV, it will be deleted by the upgrade process
	_, err = createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Create "updated" CSV
	strategyNew := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				// Same name
				Name: strategy.DeploymentSpecs[0].Name,
				// Different spec
				Spec: newNginxDeployment(nginxName),
			},
		},
	}

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
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategyNew,
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

	cleanupNewCSV, err := createCSV(t, c, crc, csvNew, testNamespace, true, false)
	require.NoError(t, err)
	defer cleanupNewCSV()

	// Wait for updated CSV to succeed
	fetchedCSV, err := fetchCSV(t, crc, csvNew.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should have updated existing deployment
	depUpdated, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depUpdated)
	require.Equal(t, depUpdated.Spec.Template.Spec.Containers[0].Name, strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name)

	// Should eventually GC the CSV
	err = waitForCSVToDelete(t, crc, csv.Name)
	require.NoError(t, err)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err := fetchCSV(t, crc, csvNew.Name, testNamespace, csvSucceededChecker)
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
	cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
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
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}

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
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
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
	_, err = createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Create "updated" CSV
	strategyNew := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep2"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}

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
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategyNew,
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

	cleanupNewCSV, err := createCSV(t, c, crc, csvNew, testNamespace, true, false)
	require.NoError(t, err)
	defer cleanupNewCSV()

	// Wait for updated CSV to succeed
	fetchedCSV, err := fetchCSV(t, crc, csvNew.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err := fetchCSV(t, crc, csvNew.Name, testNamespace, csvSucceededChecker)
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
	cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Versions: []apiextensions.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
				},
			},
			Names: apiextensions.CustomResourceDefinitionNames{
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
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}

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
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
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
	_, err = createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Create "updated" CSV
	strategyNew := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep2"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}

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
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategyNew,
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

	cleanupNewCSV, err := createCSV(t, c, crc, csvNew, testNamespace, true, false)
	require.NoError(t, err)
	defer cleanupNewCSV()

	// Wait for updated CSV to succeed
	fetchedCSV, err := fetchCSV(t, crc, csvNew.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err := fetchCSV(t, crc, csvNew.Name, testNamespace, csvSucceededChecker)
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

func TestUpdateCSVInPlace(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create dependency first (CRD)
	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"
	cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
				Plural:   crdPlural,
				Singular: crdPlural,
				Kind:     crdPlural,
				ListKind: "list" + crdPlural,
			},
			Scope: "Namespaced",
		},
	})

	// Create "current" CSV
	nginxName := genName("nginx-")
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(nginxName),
			},
		},
	}

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
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
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

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, true)
	require.NoError(t, err)
	defer cleanupCSV()

	// Wait for current CSV to succeed
	fetchedCSV, err := fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Create "updated" CSV with a different image
	strategyNew := strategy
	strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:    genName("hat"),
			Image:   "quay.io/coreos/mock-extension-apiserver:master",
			Command: []string{"/bin/mock-extension-apiserver"},
			Args: []string{
				"-v=4",
				"--mock-kinds",
				"fedora",
				"--mock-group-version",
				"group.version",
				"--secure-port",
				"5443",
				"--debug",
			},
			Ports: []corev1.ContainerPort{
				{
					ContainerPort: 5443,
				},
			},
			ImagePullPolicy: corev1.PullIfNotPresent,
		},
	}

	// Also set something outside the spec template - this should be ignored
	var five int32 = 5
	strategyNew.DeploymentSpecs[0].Spec.Replicas = &five

	require.NoError(t, err)

	fetchedCSV.Spec.InstallStrategy.StrategySpec = strategyNew

	// Update CSV directly
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Update(fetchedCSV)
	require.NoError(t, err)

	// wait for deployment spec to be updated
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
		if err != nil {
			return false, err
		}
		fmt.Println("waiting for deployment to update...")
		return fetched.Spec.Template.Spec.Containers[0].Name == strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name, nil
	})
	require.NoError(t, err)

	// Wait for updated CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	depUpdated, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depUpdated)

	// Deployment should have changed even though the CSV is otherwise the same
	require.Equal(t, depUpdated.Spec.Template.Spec.Containers[0].Name, strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Name)
	require.Equal(t, depUpdated.Spec.Template.Spec.Containers[0].Image, strategyNew.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Image)

	// Field updated even though template spec didn't change, because it was part of a template spec change as well
	require.Equal(t, *depUpdated.Spec.Replicas, *strategyNew.DeploymentSpecs[0].Spec.Replicas)
}

func TestUpdateCSVMultipleVersionCRD(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create initial CRD which has 2 versions: v1alpha1 & v1alpha2
	crdPlural := genName("ins4")
	crdName := crdPlural + ".cluster.com"
	cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Versions: []apiextensions.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
				},
				{
					Name:    "v1alpha2",
					Served:  true,
					Storage: false,
				},
			},
			Names: apiextensions.CustomResourceDefinitionNames{
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

	// create initial deployment strategy
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep1-"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}

	require.NoError(t, err)

	// First CSV with owning CRD v1alpha1
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster4",
					},
				},
			},
		},
	}

	// CSV will be deleted by the upgrade process later
	_, err = createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployment
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)

	// Create updated deployment strategy
	strategyNew := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep2-"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}

	require.NoError(t, err)

	// Second CSV with owning CRD v1alpha1 and v1alpha2
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
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategyNew,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster4",
					},
					{
						Name:        crdName,
						Version:     "v1alpha2",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster4",
					},
				},
			},
		},
	}

	// Create newly updated CSV
	_, err = createCSV(t, c, crc, csvNew, testNamespace, false, false)
	require.NoError(t, err)

	// Wait for updated CSV to succeed
	fetchedCSV, err := fetchCSV(t, crc, csvNew.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err := fetchCSV(t, crc, csvNew.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)
	compareResources(t, fetchedCSV, sameCSV)

	// Should have created new deployment and deleted old one
	depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depNew)
	err = waitForDeploymentToDelete(t, c, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)

	// Create updated deployment strategy
	strategyNew2 := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep3-"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}
	require.NoError(t, err)

	// Third CSV with owning CRD v1alpha2
	csvNew2 := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv3"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces: csvNew.Name,
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategyNew2,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:        crdName,
						Version:     "v1alpha2",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster4",
					},
				},
			},
		},
	}

	// Create newly updated CSV
	cleanupNewCSV, err := createCSV(t, c, crc, csvNew2, testNamespace, true, false)
	require.NoError(t, err)
	defer cleanupNewCSV()

	// Wait for updated CSV to succeed
	fetchedCSV, err = fetchCSV(t, crc, csvNew2.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Fetch cluster service version again to check for unnecessary control loops
	sameCSV, err = fetchCSV(t, crc, csvNew2.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)
	compareResources(t, fetchedCSV, sameCSV)

	// Should have created new deployment and deleted old one
	depNew, err = c.GetDeployment(testNamespace, strategyNew2.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depNew)
	err = waitForDeploymentToDelete(t, c, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)

	// Should clean up the CSV
	err = waitForCSVToDelete(t, crc, csvNew.Name)
	require.NoError(t, err)
}

func TestUpdateCSVModifyDeploymentName(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create dependency first (CRD)
	crdPlural := genName("ins2")
	crdName := crdPlural + ".cluster.com"
	cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
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
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
			{
				Name: "dep2-test",
				Spec: newNginxDeployment("nginx2"),
			},
		},
	}

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
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
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

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, true, false)
	require.NoError(t, err)
	defer cleanupCSV()

	// Wait for current CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created deployments
	dep, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, dep)
	dep2, err := c.GetDeployment(testNamespace, strategy.DeploymentSpecs[1].Name)
	require.NoError(t, err)
	require.NotNil(t, dep2)

	// Create "updated" CSV
	strategyNew := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep3-"),
				Spec: newNginxDeployment(genName("nginx3-")),
			},
			{
				Name: "dep2-test",
				Spec: newNginxDeployment("nginx2"),
			},
		},
	}

	require.NoError(t, err)

	// Fetch the current csv
	fetchedCSV, err := fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Update csv with same strategy with different deployment's name
	fetchedCSV.Spec.InstallStrategy.StrategySpec = strategyNew

	// Update the current csv with the new csv
	_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Update(fetchedCSV)
	require.NoError(t, err)

	// Wait for new deployment to exist
	err = waitForDeployment(t, c, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)

	// Wait for updated CSV to succeed
	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Should have created new deployment and deleted old
	depNew, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[0].Name)
	require.NoError(t, err)
	require.NotNil(t, depNew)
	// Make sure the unchanged deployment still exists
	depNew2, err := c.GetDeployment(testNamespace, strategyNew.DeploymentSpecs[1].Name)
	require.NoError(t, err)
	require.NotNil(t, depNew2)
	err = waitForDeploymentToDelete(t, c, strategy.DeploymentSpecs[0].Name)
	require.NoError(t, err)
}

func TestCreateCSVRequirementsEvents(t *testing.T) {
	t.Skip()
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	sa := corev1.ServiceAccount{}
	sa.SetName(genName("sa-"))
	sa.SetNamespace(testNamespace)
	_, err := c.CreateServiceAccount(&sa)
	require.NoError(t, err, "could not create ServiceAccount")

	permissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: sa.GetName(),
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
				{
					Verbs:     []string{"delete"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		},
	}

	clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
		{
			ServiceAccountName: sa.GetName(),
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"deployment"},
				},
			},
		},
	}

	depName := genName("dep-")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("csv"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: newNginxInstallStrategy(depName, permissions, clusterPermissions),
			// Cheating a little; this is an APIservice that will exist for the e2e tests
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Required: []v1alpha1.APIServiceDescription{
					{
						Group:       "packages.operators.coreos.com",
						Version:     "v1",
						Kind:        "PackageManifest",
						DisplayName: "Package Manifest",
						Description: "An apiservice that exists",
					},
				},
			},
		},
	}

	// Create Role/Cluster Roles and RoleBindings
	role := rbacv1.Role{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"create"},
				APIGroups: []string{""},
				Resources: []string{"deployment"},
			},
			{
				Verbs:     []string{"delete"},
				APIGroups: []string{""},
				Resources: []string{"deployment"},
			},
		},
	}
	role.SetName("test-role")
	role.SetNamespace(testNamespace)
	_, err = c.CreateRole(&role)
	require.NoError(t, err, "could not create Role")

	roleBinding := rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     role.GetName(),
		},
	}
	roleBinding.SetName(genName("dep-"))
	roleBinding.SetNamespace(testNamespace)
	_, err = c.CreateRoleBinding(&roleBinding)
	require.NoError(t, err, "could not create RoleBinding")

	clusterRole := rbacv1.ClusterRole{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get"},
				APIGroups: []string{""},
				Resources: []string{"deployment"},
			},
		},
	}
	clusterRole.SetName(genName("dep-"))
	_, err = c.CreateClusterRole(&clusterRole)
	require.NoError(t, err, "could not create ClusterRole")

	clusterRoleBinding := rbacv1.ClusterRoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     clusterRole.GetName(),
		},
	}
	clusterRoleBinding.SetName(genName("dep-"))
	_, err = c.CreateClusterRoleBinding(&clusterRoleBinding)
	require.NoError(t, err, "could not create ClusterRoleBinding")

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, false, false)
	require.NoError(t, err)
	defer cleanupCSV()

	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	listOptions := metav1.ListOptions{
		FieldSelector: "involvedObject.kind=ClusterServiceVersion",
	}

	// Get events from test namespace for CSV
	eventsList, err := c.KubernetesInterface().CoreV1().Events(testNamespace).List(listOptions)
	require.NoError(t, err)
	latestEvent := findLastEvent(eventsList)
	require.Equal(t, string(latestEvent.Reason), "InstallSucceeded")

	// Edit role
	updatedRole := rbacv1.Role{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"create"},
				APIGroups: []string{""},
				Resources: []string{"deployment"},
			},
		},
	}
	updatedRole.SetName("test-role")
	updatedRole.SetNamespace(testNamespace)
	_, err = c.UpdateRole(&updatedRole)
	require.NoError(t, err)

	// Check CSV status
	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvPendingChecker)
	require.NoError(t, err)

	// Check event
	eventsList, err = c.KubernetesInterface().CoreV1().Events(testNamespace).List(listOptions)
	require.NoError(t, err)
	latestEvent = findLastEvent(eventsList)
	require.Equal(t, string(latestEvent.Reason), "RequirementsNotMet")

	// Reverse the updated role
	_, err = c.UpdateRole(&role)
	require.NoError(t, err)

	// Check CSV status
	_, err = fetchCSV(t, crc, csv.Name, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Check event
	eventsList, err = c.KubernetesInterface().CoreV1().Events(testNamespace).List(listOptions)
	require.NoError(t, err)
	latestEvent = findLastEvent(eventsList)
	require.Equal(t, string(latestEvent.Reason), "InstallSucceeded")
}

// TODO: test behavior when replaces field doesn't point to existing CSV

func TestCSVStatusInvalidCSV(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create CRD
	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"
	cleanupCRD, err := createCRD(c, apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
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

	// create CSV
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}

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
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:        crdName,
						Version:     "apiextensions.k8s.io/v1alpha1", // purposely invalid, should be just v1alpha1 to match CRD
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: "In the cluster2",
					},
				},
			},
		},
	}

	cleanupCSV, err := createCSV(t, c, crc, csv, testNamespace, true, false)
	require.NoError(t, err)
	defer cleanupCSV()

	notServedStatus := v1alpha1.RequirementStatus{
		Group:   "apiextensions.k8s.io",
		Version: "v1beta1",
		Kind:    "CustomResourceDefinition",
		Name:    crdName,
		Status:  v1alpha1.RequirementStatusReasonNotPresent,
		Message: "CRD version not served",
	}
	csvCheckPhaseAndRequirementStatus := func(csv *v1alpha1.ClusterServiceVersion) bool {
		if csv.Status.Phase == v1alpha1.CSVPhasePending {
			for _, status := range csv.Status.RequirementStatus {
				if status.Message == notServedStatus.Message {
					return true
				}
			}
		}
		return false
	}

	fetchedCSV, err := fetchCSV(t, crc, csv.Name, testNamespace, csvCheckPhaseAndRequirementStatus)
	require.NoError(t, err)

	require.Contains(t, fetchedCSV.Status.RequirementStatus, notServedStatus)
}
