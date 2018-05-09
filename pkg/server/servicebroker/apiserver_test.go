package servicebroker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateBrokerAPIVersion(t *testing.T) {
	// test bad version
	brokerMock := &ALMBroker{}
	err := brokerMock.ValidateBrokerAPIVersion("oops")
	require.EqualError(t, err, "unknown OpenServiceBroker API Version: oops")

	// supported version
	require.NoError(t, brokerMock.ValidateBrokerAPIVersion("2.11"))
	require.NoError(t, brokerMock.ValidateBrokerAPIVersion("2.12"))
	require.NoError(t, brokerMock.ValidateBrokerAPIVersion("2.13"))
}
