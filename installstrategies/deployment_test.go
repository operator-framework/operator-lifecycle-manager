package installstrategies

//
//import (
//	"fmt"
//	"testing"
//
//	"github.com/coreos-inc/operator-client/pkg/client"
//	"github.com/golang/mock/gomock"
//	"github.com/stretchr/testify/assert"
//	v1beta1extensions "k8s.io/api/extensions/v1beta1"
//	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
//	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
//)
//
//func TestKubeDeployment(t *testing.T) {
//	testDeploymentName := "alm-test-deployment"
//	testDeploymentNamespace := "alm-test"
//	testDeploymentLabels := map[string]string{"app": "alm", "env": "test"}
//
//	mockOwner := metav1.ObjectMeta{
//		Name:         "operatorversion-owner",
//		Namespace:    testDeploymentNamespace,
//		GenerateName: fmt.Sprintf("%s-", testDeploymentNamespace),
//	}
//
//	unstructuredDep := &unstructured.Unstructured{}
//	unstructuredDep.SetName(testDeploymentName)
//	unstructuredDep.SetNamespace("not-the-same-namespace")
//	unstructuredDep.SetLabels(testDeploymentLabels)
//
//	labels := map[string]string{
//		"alm-owned":           "true",
//		"alm-owner-name":      mockOwner.Name,
//		"alm-owner-namespace": mockOwner.Namespace,
//	}
//	deployment := v1beta1extensions.Deployment{
//		ObjectMeta: metav1.ObjectMeta{
//			Namespace:    testDeploymentNamespace,
//			GenerateName: fmt.Sprintf("%s-", mockOwner.Name),
//			Labels:       labels,
//		},
//	}
//
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	mockClient := client.NewMockInterface(ctrl)
//	mockClient.EXPECT().
//		ListDeploymentsWithLabels(testDeploymentNamespace, labels).
//		Return(&v1beta1extensions.DeploymentList{}, nil)
//	mockClient.EXPECT().
//		CreateDeployment(&deployment).
//		Return(&deployment, nil)
//
//	deployInstallStrategy := &StrategyDetailsDeployment{[]v1beta1extensions.DeploymentSpec{deployment.Spec}}
//	assert.NoError(t, deployInstallStrategy.Install(mockClient, mockOwner))
//}
