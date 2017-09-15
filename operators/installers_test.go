package alm

import (
	"testing"

	"github.com/coreos-inc/operator-client/pkg/client"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	v1beta1extensions "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestKubeDeployment(t *testing.T) {
	testDeploymentName := "alm-test-deployment"
	testDeploymentNamespace := "alm-test"
	testDeploymentLabels := map[string]string{"app": "alm", "env": "test"}

	unstructuredDep := &unstructured.Unstructured{}
	unstructuredDep.SetName(testDeploymentName)
	unstructuredDep.SetNamespace("not-the-same-namespace")
	unstructuredDep.SetLabels(testDeploymentLabels)

	deployment := v1beta1extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDeploymentName,
			Namespace: testDeploymentNamespace,
			Labels:    testDeploymentLabels,
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := client.NewMockInterface(ctrl)

	mockClient.EXPECT().
		CreateDeployment(&deployment).
		Return(&deployment, nil)

	kubeDeployer := &KubeDeployment{client: mockClient}
	assert.NoError(t, kubeDeployer.Install(testDeploymentNamespace, unstructuredDep))
}
