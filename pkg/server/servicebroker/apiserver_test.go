package servicebroker

import (
	"testing"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	gomock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
)

const (
	testNamespace = "testns"
)

func mockALMBroker(ctrl *gomock.Controller) *ALMBroker {
	return &ALMBroker{
		opClient:  opClient.NewMockInterface(ctrl),
		client:    fake.NewSimpleClientset(),
		namespace: testNamespace,
	}
}

func TestValidateBrokerAPIVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	err := mockALMBroker(ctrl).ValidateBrokerAPIVersion("n/a")
	require.EqualError(t, err, "not implemented")
}

func TestGetCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl).GetCatalog(nil)
	require.EqualError(t, err, "not implemented")
}

func TestProvision(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl).Provision(nil, nil)
	require.EqualError(t, err, "not implemented")
}

func TestDeprovision(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl).Deprovision(nil, nil)
	require.EqualError(t, err, "not implemented")
}

func TestLastOperation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl).LastOperation(nil, nil)
	require.EqualError(t, err, "not implemented")
}

func TestBind(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl).Bind(nil, nil)
	require.EqualError(t, err, "not implemented")
}

func TestUnbind(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl).Unbind(nil, nil)
	require.EqualError(t, err, "not implemented")
}

func TestUpdate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl).Update(nil, nil)
	require.EqualError(t, err, "not implemented")
}
