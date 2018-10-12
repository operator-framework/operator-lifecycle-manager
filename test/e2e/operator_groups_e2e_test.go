package e2e

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"

	"github.com/coreos/go-semver/semver"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO: make tests for:
// RBAC
// Deployment annotation verification

// func NoTestCreateOperatorGroupWithMatchingNamespace(t *testing.T) {
// 	// Create namespace with specific label
// 	// Create deployment in namespace
// 	// Create operator group that watches namespace and uses specific label
// 	// Verify operator group status contains correct status
// 	// Verify deployments have correct namespace annotation

// 	// c := newKubeClient(t)

// 	// deployment := appsv1.

// 	// c.CreateDeployment()

// }

func TestCreateOperatorCSVCopy(t *testing.T) {
	// create operator namespace
	// create operator group in OLM namespace
	// create CSV in OLM namespace
	// verify CSV is copied to operator namespace

	log.SetLevel(log.DebugLevel)
	c := newKubeClient(t)
	crc := newCRClient(t)
	operatorNamespaceName := testNamespace + "-operator"
	csvName := "acsv-that-is-unique" // must be lowercase for DNS-1123 validation
	matchingLabel := map[string]string{"app": "matchLabel"}

	operatorNamespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   operatorNamespaceName,
			Labels: matchingLabel,
		},
	}
	_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(&operatorNamespace)
	require.NoError(t, err)

	operatorGroup := v1alpha2.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-operator-group",
			Namespace: testNamespace,
		},
		Spec: v1alpha2.OperatorGroupSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: matchingLabel,
			},
		},
	}
	_, err = crc.OperatorsV1alpha2().OperatorGroups(testNamespace).Create(&operatorGroup)
	require.NoError(t, err)

	aCSV := newCSV(csvName, testNamespace, "", *semver.New("0.0.0"), nil, nil, newNginxInstallStrategy("aspec", nil, nil))
	createdCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(&aCSV)
	require.NoError(t, err)

	var csvCopy *v1alpha1.ClusterServiceVersion
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		csvCopy, err = crc.OperatorsV1alpha1().ClusterServiceVersions(operatorNamespaceName).Get(csvName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	require.Equal(t, createdCSV.Name, csvCopy.Name)
	require.Equal(t, createdCSV.Spec, csvCopy.Spec)

	// clean up
	err = c.KubernetesInterface().CoreV1().Namespaces().Delete(operatorNamespaceName, &metav1.DeleteOptions{})
	if err != nil {
		t.Errorf("Operator namespace cleanup failed: %v\n", err)
	}
}
