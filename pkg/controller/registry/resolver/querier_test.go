package resolver

import (
	"context"
	"fmt"
	"testing"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/fakes"
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

func TestNamespaceSourceQuerier_FindProvider(t *testing.T) {
	fakeSource := fakes.FakeClientInterface{}
	fakeSource2 := fakes.FakeClientInterface{}
	sources := map[registry.CatalogKey]registry.ClientInterface{
		registry.CatalogKey{"test", "ns"}:  &fakeSource,
		registry.CatalogKey{"test2", "ns"}: &fakeSource2,
	}
	excludedPkgs := make(map[string]struct{})
	bundle := &api.Bundle{CsvName: "test", PackageName: "testPkg", ChannelName: "testChannel"}
	bundle2 := &api.Bundle{CsvName: "test2", PackageName: "testPkg2", ChannelName: "testChannel2"}
	fakeSource.GetBundleThatProvidesStub = func(ctx context.Context, group, version, kind string) (*api.Bundle, error) {
		if group != "group" || version != "version" || kind != "kind" {
			return nil, fmt.Errorf("Not Found")
		}
		return bundle, nil
	}
	fakeSource2.GetBundleThatProvidesStub = func(ctx context.Context, group, version, kind string) (*api.Bundle, error) {
		if group != "group2" || version != "version2" || kind != "kind2" {
			return nil, fmt.Errorf("Not Found")
		}
		return bundle2, nil
	}
	fakeSource.FindBundleThatProvidesStub = func(ctx context.Context, group, version, kind string, excludedPkgs map[string]struct{}) (*api.Bundle, error) {
		if group != "group" || version != "version" || kind != "kind" {
			return nil, fmt.Errorf("Not Found")
		}
		return bundle, nil
	}
	fakeSource2.FindBundleThatProvidesStub = func(ctx context.Context, group, version, kind string, excludedPkgs map[string]struct{}) (*api.Bundle, error) {
		if group != "group2" || version != "version2" || kind != "kind2" {
			return nil, fmt.Errorf("Not Found")
		}
		return bundle2, nil
	}

	type fields struct {
		sources map[registry.CatalogKey]registry.ClientInterface
	}
	type args struct {
		api        opregistry.APIKey
		catalogKey registry.CatalogKey
	}
	type out struct {
		bundle *api.Bundle
		key    *registry.CatalogKey
		err    error
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		out    out
	}{
		{
			fields: fields{
				sources: sources,
			},
			args: args{
				api:        opregistry.APIKey{"group", "version", "kind", "plural"},
				catalogKey: registry.CatalogKey{},
			},
			out: out{
				bundle: bundle,
				key:    &registry.CatalogKey{Name: "test", Namespace: "ns"},
				err:    nil,
			},
		},
		{
			fields: fields{
				sources: nil,
			},
			args: args{
				api:        opregistry.APIKey{"group", "version", "kind", "plural"},
				catalogKey: registry.CatalogKey{},
			},
			out: out{
				bundle: nil,
				key:    nil,
				err:    fmt.Errorf("group/version/kind (plural) not provided by a package in any CatalogSource"),
			},
		},
		{
			fields: fields{
				sources: sources,
			},
			args: args{
				api:        opregistry.APIKey{"group2", "version2", "kind2", "plural2"},
				catalogKey: registry.CatalogKey{Name: "test2", Namespace: "ns"},
			},
			out: out{
				bundle: bundle2,
				key:    &registry.CatalogKey{Name: "test2", Namespace: "ns"},
				err:    nil,
			},
		},
		{
			fields: fields{
				sources: sources,
			},
			args: args{
				api:        opregistry.APIKey{"group2", "version2", "kind2", "plural2"},
				catalogKey: registry.CatalogKey{Name: "test3", Namespace: "ns"},
			},
			out: out{
				bundle: bundle2,
				key:    &registry.CatalogKey{Name: "test2", Namespace: "ns"},
				err:    nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &NamespaceSourceQuerier{
				sources: tt.fields.sources,
			}
			bundle, key, err := q.FindProvider(tt.args.api, tt.args.catalogKey, excludedPkgs)
			require.Equal(t, tt.out.err, err)
			require.Equal(t, tt.out.bundle, bundle)
			require.Equal(t, tt.out.key, key)
		})
	}
}

func TestNamespaceSourceQuerier_FindPackage(t *testing.T) {
	initialSource := fakes.FakeClientInterface{}
	otherSource := fakes.FakeClientInterface{}
	initalBundle := &api.Bundle{CsvName: "test", PackageName: "testPkg", ChannelName: "testChannel"}
	startingBundle := &api.Bundle{CsvName: "starting-test", PackageName: "testPkg", ChannelName: "testChannel"}
	otherBundle := &api.Bundle{CsvName: "other", PackageName: "otherPkg", ChannelName: "otherChannel"}
	initialSource.GetBundleStub = func(ctx context.Context, pkgName, channelName, csvName string) (*api.Bundle, error) {
		if csvName != startingBundle.CsvName {
			return nil, fmt.Errorf("not found")
		}
		return startingBundle, nil
	}
	initialSource.GetBundleInPackageChannelStub = func(ctx context.Context, pkgName, channelName string) (*api.Bundle, error) {
		if pkgName != initalBundle.CsvName {
			return nil, fmt.Errorf("not found")
		}
		return initalBundle, nil
	}
	otherSource.GetBundleInPackageChannelStub = func(ctx context.Context, pkgName, channelName string) (*api.Bundle, error) {
		if pkgName != otherBundle.CsvName {
			return nil, fmt.Errorf("not found")
		}
		return otherBundle, nil
	}
	initialKey := registry.CatalogKey{"initial", "ns"}
	otherKey := registry.CatalogKey{"other", "other"}
	sources := map[registry.CatalogKey]registry.ClientInterface{
		initialKey: &initialSource,
		otherKey:   &otherSource,
	}

	type fields struct {
		sources map[registry.CatalogKey]registry.ClientInterface
	}
	type args struct {
		pkgName       string
		channelName   string
		startingCSV   string
		initialSource registry.CatalogKey
	}
	type out struct {
		bundle *api.Bundle
		key    *registry.CatalogKey
		err    error
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		out    out
	}{
		{
			name:   "Initial/Found",
			fields: fields{sources: sources},
			args:   args{"test", "testChannel", "", registry.CatalogKey{"initial", "ns"}},
			out:    out{bundle: initalBundle, key: &initialKey, err: nil},
		},
		{
			name:   "Initial/CatalogNotFound",
			fields: fields{sources: sources},
			args:   args{"test", "testChannel", "", registry.CatalogKey{"absent", "found"}},
			out:    out{bundle: nil, key: nil, err: fmt.Errorf("CatalogSource {absent found} not found")},
		},
		{
			name:   "Initial/StartingCSVFound",
			fields: fields{sources: sources},
			args:   args{"test", "testChannel", "starting-test", registry.CatalogKey{"initial", "ns"}},
			out:    out{bundle: startingBundle, key: &initialKey, err: nil},
		},
		{
			name:   "Initial/StartingCSVNotFound",
			fields: fields{sources: sources},
			args:   args{"test", "testChannel", "non-existent", registry.CatalogKey{"initial", "ns"}},
			out:    out{bundle: nil, key: nil, err: fmt.Errorf("not found")},
		},
		{
			name:   "Other/Found",
			fields: fields{sources: sources},
			args:   args{"other", "testChannel", "", registry.CatalogKey{"", ""}},
			out:    out{bundle: otherBundle, key: &otherKey, err: nil},
		},
		{
			name:   "NotFound",
			fields: fields{sources: sources},
			args:   args{"nope", "not", "", registry.CatalogKey{"", ""}},
			out:    out{bundle: nil, err: fmt.Errorf("nope/not not found in any available CatalogSource")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &NamespaceSourceQuerier{
				sources: tt.fields.sources,
			}
			var got *api.Bundle
			var key *registry.CatalogKey
			var err error
			if tt.args.startingCSV != "" {
				got, key, err = q.FindBundle(tt.args.pkgName, tt.args.channelName, tt.args.startingCSV, tt.args.initialSource)
			} else {
				got, key, err = q.FindLatestBundle(tt.args.pkgName, tt.args.channelName, tt.args.initialSource)
			}
			require.Equal(t, tt.out.err, err)
			require.Equal(t, tt.out.bundle, got)
			require.Equal(t, tt.out.key, key)
		})
	}
}
