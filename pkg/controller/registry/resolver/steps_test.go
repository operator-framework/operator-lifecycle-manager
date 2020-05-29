package resolver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSubscriptionStepResource(t *testing.T) {
	info := OperatorSourceInfo{
		Package:     "V1",
		Channel:     "Default",
		StartingCSV: "csv.v1.0.0",
		Catalog: CatalogKey{
			Name:      "catalogName",
			Namespace: "CatalogNamespace",
		},
	}

	actual, err := NewSubscriptionStepResource("namespace", info)
	require.NoError(t, err)
	require.Equal(t, "v1-default-catalogname-catalognamespace", actual.Name)
}
