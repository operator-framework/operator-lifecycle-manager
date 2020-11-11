package resolver

import (
	"regexp"
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/stretchr/testify/require"
)

func TestNewSubscriptionStepResource(t *testing.T) {

	tests := []struct {
		name string
		info OperatorSourceInfo
		want string
	}{
		{
			"upper case",
			OperatorSourceInfo{
				Package:     "TestPackage",
				Channel:     "testChannel",
				StartingCSV: "csv.v1.0.0",
				Catalog: registry.CatalogKey{
					Name:      "CatalogName",
					Namespace: "CatalogNamespace",
				},
			},
			"testpackage-testchannel-catalogname-catalognamespace-[0-9a-z]{5}",
		},
		{
			"very long names",
			OperatorSourceInfo{
				Package:     "TestPackage",
				Channel:     "verylongtestChannel",
				StartingCSV: "csv.v1.0.0",
				Catalog: registry.CatalogKey{
					Name:      "VeryLongCatalogName",
					Namespace: "VeryLongCatalogNamespace",
				},
			},
			"testpackage-verylongtestchannel-verylongcatalogname-verylo[0-9a-z]{5}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewSubscriptionStepResource("namespace", tt.info)
			require.NoError(t, err)
			require.Less(t, len(got.Name), 64)
			require.Regexp(t, regexp.MustCompile(tt.want), got.Name)
		})
	}
}
