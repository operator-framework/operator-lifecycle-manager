package rh_operators

import (
	"fmt"
	"os"
	"testing"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	pclient "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client"
	psVersioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	kubeconfig = os.Getenv("KUBECONFIG")
	testlog    = logrus.New()
)

// TestRHOperators installs all Red Hat Operators available on the cluster and see that they install successfully.
func TestRHOperators(t *testing.T) {
	c := operatorclient.NewClientFromConfig(kubeconfig, testlog)

	crc, err := pclient.NewClient(kubeconfig)
	require.NoError(t, err, "error creating package-server client")

	vc, err := client.NewClient(kubeconfig)
	require.NoError(t, err, "error creating cr client")

	// Serial Test operators with installModes other than typeOwnNamespace to avoid OperatorGroup intersection.
	operatorList, err := ListRHOperatorsWithoutInstallModes(crc, v1alpha1.InstallModeTypeOwnNamespace)
	require.NoError(t, err, "failed to list Red Hat Operators")

	for _, operator := range operatorList {
		operatorName := operator
		t.Run(fmt.Sprintf("Testing for operator %s", operator), func(t *testing.T) {
			testInstallOperators(t, c, crc, vc, operatorName)
		})
	}

	CleanupAll(t, c, vc)

	operatorList, err = ListRHOperatorsByInstallModes(crc, v1alpha1.InstallModeTypeOwnNamespace)
	require.NoError(t, err, "failed to list Red Hat Operators with OwnNamespace InstallMode")

	for _, operator := range operatorList {
		operatorName := operator
		t.Run(fmt.Sprintf("Testing for operator %s", operator), func(t *testing.T) {
			t.Parallel()
			testInstallOperators(t, c, crc, vc, operatorName)
		})
	}

	CleanupAll(t, c, vc)
}

// testInstallOperators installs operators and wait till its CSV is in succeeded phase then deletes created resources
// and wait till the created Namespace is terminated.
func testInstallOperators(t *testing.T, c operatorclient.ClientInterface, crc psVersioned.Interface,
	vc versioned.Interface, operatorName string) {
	t.Logf("Installing %s operator", operatorName)

	o, err := NewOperator(c, crc, vc, operatorName)
	if err != nil {
		assert.Failf(t, err.Error(), "failed to load operator %s from packagemanifests", operatorName)
		return
	}

	err = o.Subscribe()
	assert.NoError(t, err, "error creating subscription for operator %s", operatorName)

	err = o.Unsubscribe(false)
	assert.NoError(t, err, "error cleaning up subscription for operator %s", operatorName)
	if err != nil {
		CleanupOperatorCSVs(t, o)
		CleanupOperatorNamespace(t, o)
	}

	err = o.WaitToDeleteNamespace()
	if err != nil {
		t.Logf("error cleaning up Namespace: %s, %v", o.namespace, err)
	}
}
