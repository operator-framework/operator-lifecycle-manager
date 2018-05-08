package servicebroker

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
)

const (
	testNamespace = "testns"
)

func mockALMBroker(ctrl *gomock.Controller) *ALMBroker {
	return &ALMBroker{
		client:    fake.NewSimpleClientset(),
		namespace: testNamespace,
	}
}

func TestValidateBrokerAPIVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	// test bad version
	brokerMock := mockALMBroker(ctrl)
	err := brokerMock.ValidateBrokerAPIVersion("oops")
	require.EqualError(t, err, "unknown OpenServiceBroker API Version: oops")

	// supported version
	require.NoError(t, brokerMock.ValidateBrokerAPIVersion("2.11"))
	require.NoError(t, brokerMock.ValidateBrokerAPIVersion("2.12"))
	require.NoError(t, brokerMock.ValidateBrokerAPIVersion("2.13"))
}
