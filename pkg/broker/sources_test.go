package broker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewALMBroker(t *testing.T) {
	bs, err := NewALMBroker("", Options{})
	require.NoError(t, err)
	require.NotNil(t, bs)
}
