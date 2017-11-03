package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirectoryLoader(t *testing.T) {
	_, err := NewInMemoryFromDirectory("../../catalog_resources")
	require.NoError(t, err)
}
