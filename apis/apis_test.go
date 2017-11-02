package apis

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupName(t *testing.T) {
	require.Equal(t, GroupName, "app.coreos.com")
}
