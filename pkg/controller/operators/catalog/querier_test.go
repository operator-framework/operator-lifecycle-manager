package catalog

import (
	"fmt"
	"testing"

	"github.com/operator-framework/operator-registry/pkg/client"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog/fakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

func TestNewNamespaceSourceQuerier(t *testing.T) {
	emptySources := map[registry.CatalogKey]registry.ClientInterface{}
	nonEmptySources := map[registry.CatalogKey]registry.ClientInterface{
		registry.CatalogKey{"test", "ns"}: &registry.Client{
			Client: &client.Client{
				Registry: &fakes.FakeRegistryClient{},
			},
		},
	}

	type args struct {
		sources map[registry.CatalogKey]registry.ClientInterface
	}
	tests := []struct {
		name string
		args args
		want *NamespaceSourceQuerier
	}{
		{
			name: "nil",
			args: args{
				sources: nil,
			},
			want: &NamespaceSourceQuerier{sources: nil},
		},
		{
			name: "empty",
			args: args{
				sources: emptySources,
			},
			want: &NamespaceSourceQuerier{sources: emptySources},
		},
		{
			name: "nonEmpty",
			args: args{
				sources: nonEmptySources,
			},
			want: &NamespaceSourceQuerier{sources: nonEmptySources},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, NewNamespaceSourceQuerier(tt.args.sources), tt.want)
		})
	}
}

func TestNamespaceSourceQuerier_Queryable(t *testing.T) {
	type fields struct {
		sources map[registry.CatalogKey]registry.ClientInterface
	}
	tests := []struct {
		name   string
		fields fields
		error  error
	}{
		{
			name: "nil",
			fields: fields{
				sources: nil,
			},
			error: fmt.Errorf("no catalog sources available"),
		},
		{
			name: "empty",
			fields: fields{
				sources: map[registry.CatalogKey]registry.ClientInterface{},
			},
			error: fmt.Errorf("no catalog sources available"),
		},
		{
			name: "nonEmpty",
			fields: fields{
				sources: map[registry.CatalogKey]registry.ClientInterface{
					registry.CatalogKey{"test", "ns"}: &registry.Client{
						Client: &client.Client{
							Registry: &fakes.FakeRegistryClient{},
						},
					},
				},
			},
			error: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &NamespaceSourceQuerier{
				sources: tt.fields.sources,
			}
			require.Equal(t, q.Queryable(), tt.error)
		})
	}
}
