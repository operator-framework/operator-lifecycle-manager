package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/lib/version"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
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

func TestNamespaceSourceQuerier_FindReplacement(t *testing.T) {
	// TODO: clean up this test setup
	initialSource := fakes.FakeClientInterface{}
	otherSource := fakes.FakeClientInterface{}
	replacementSource := fakes.FakeClientInterface{}
	replacementAndLatestSource := fakes.FakeClientInterface{}
	replacementAndNoAnnotationLatestSource := fakes.FakeClientInterface{}

	latestVersion := semver.MustParse("1.0.0-1556661308")
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "latest",
			Namespace: "placeholder",
			Annotations: map[string]string{
				"olm.skipRange": ">= 1.0.0-0 < 1.0.0-1556661308",
			},
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    []v1alpha1.CRDDescription{},
				Required: []v1alpha1.CRDDescription{},
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned:    []v1alpha1.APIServiceDescription{},
				Required: []v1alpha1.APIServiceDescription{},
			},
			Version: version.OperatorVersion{latestVersion},
		},
	}
	csvJson, err := json.Marshal(csv)
	require.NoError(t, err)

	nextBundle := &api.Bundle{CsvName: "test.v1", PackageName: "testPkg", ChannelName: "testChannel"}
	latestBundle := &api.Bundle{CsvName: "latest", PackageName: "testPkg", ChannelName: "testChannel", CsvJson: string(csvJson), Object: []string{string(csvJson)}, SkipRange: ">= 1.0.0-0 < 1.0.0-1556661308", Version: latestVersion.String()}

	csv.SetAnnotations(map[string]string{})
	csvUnstNoAnnotationJson, err := json.Marshal(csv)
	require.NoError(t, err)
	latestBundleNoAnnotation := &api.Bundle{CsvName: "latest", PackageName: "testPkg", ChannelName: "testChannel", CsvJson: string(csvUnstNoAnnotationJson), Object: []string{string(csvUnstNoAnnotationJson)}}

	initialSource.GetReplacementBundleInPackageChannelStub = func(ctx context.Context, bundleName, pkgName, channelName string) (*api.Bundle, error) {
		return nil, fmt.Errorf("not found")
	}
	replacementSource.GetReplacementBundleInPackageChannelStub = func(ctx context.Context, bundleName, pkgName, channelName string) (*api.Bundle, error) {
		return nextBundle, nil
	}
	initialSource.GetBundleInPackageChannelStub = func(ctx context.Context, pkgName, channelName string) (*api.Bundle, error) {
		if pkgName != latestBundle.PackageName {
			return nil, fmt.Errorf("not found")
		}
		return latestBundle, nil
	}
	otherSource.GetBundleInPackageChannelStub = func(ctx context.Context, pkgName, channelName string) (*api.Bundle, error) {
		if pkgName != latestBundle.PackageName {
			return nil, fmt.Errorf("not found")
		}
		return latestBundle, nil
	}
	replacementAndLatestSource.GetReplacementBundleInPackageChannelStub = func(ctx context.Context, bundleName, pkgName, channelName string) (*api.Bundle, error) {
		return nextBundle, nil
	}
	replacementAndLatestSource.GetBundleInPackageChannelStub = func(ctx context.Context, pkgName, channelName string) (*api.Bundle, error) {
		return latestBundle, nil
	}
	replacementAndNoAnnotationLatestSource.GetReplacementBundleInPackageChannelStub = func(ctx context.Context, bundleName, pkgName, channelName string) (*api.Bundle, error) {
		return nextBundle, nil
	}
	replacementAndNoAnnotationLatestSource.GetBundleInPackageChannelStub = func(ctx context.Context, pkgName, channelName string) (*api.Bundle, error) {
		return latestBundleNoAnnotation, nil
	}

	initialKey := registry.CatalogKey{"initial", "ns"}
	otherKey := registry.CatalogKey{"other", "other"}
	replacementKey := registry.CatalogKey{"replacement", "ns"}
	replacementAndLatestKey := registry.CatalogKey{"replat", "ns"}
	replacementAndNoAnnotationLatestKey := registry.CatalogKey{"replatbad", "ns"}

	sources := map[registry.CatalogKey]registry.ClientInterface{
		initialKey:                          &initialSource,
		otherKey:                            &otherSource,
		replacementKey:                      &replacementSource,
		replacementAndLatestKey:             &replacementAndLatestSource,
		replacementAndNoAnnotationLatestKey: &replacementAndNoAnnotationLatestSource,
	}

	startVersion := semver.MustParse("1.0.0-0")
	notInRange := semver.MustParse("1.0.0-1556661347")

	type fields struct {
		sources map[registry.CatalogKey]registry.ClientInterface
	}
	type args struct {
		currentVersion *semver.Version
		pkgName        string
		channelName    string
		bundleName     string
		initialSource  registry.CatalogKey
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
			name:   "FindsLatestInPrimaryCatalog",
			fields: fields{sources: sources},
			args:   args{&startVersion, "testPkg", "testChannel", "test.v1", initialKey},
			out:    out{bundle: latestBundle, key: &initialKey, err: nil},
		},
		{
			name:   "FindsLatestInSecondaryCatalog",
			fields: fields{sources: sources},
			args:   args{&startVersion, "testPkg", "testChannel", "test.v1", otherKey},
			out:    out{bundle: latestBundle, key: &otherKey, err: nil},
		},
		{
			name:   "PrefersLatestToReplaced/SameCatalog",
			fields: fields{sources: sources},
			args:   args{&startVersion, "testPkg", "testChannel", "test.v1", replacementAndLatestKey},
			out:    out{bundle: latestBundle, key: &replacementAndLatestKey, err: nil},
		},
		{
			name:   "PrefersLatestToReplaced/OtherCatalog",
			fields: fields{sources: sources},
			args:   args{&startVersion, "testPkg", "testChannel", "test.v1", initialKey},
			out:    out{bundle: latestBundle, key: &initialKey, err: nil},
		},
		{
			name:   "IgnoresLatestWithoutAnnotation",
			fields: fields{sources: sources},
			args:   args{&startVersion, "testPkg", "testChannel", "test.v1", replacementAndNoAnnotationLatestKey},
			out:    out{bundle: nextBundle, key: &replacementAndNoAnnotationLatestKey, err: nil},
		},
		{
			name:   "IgnoresLatestNotInRange",
			fields: fields{sources: sources},
			args:   args{&notInRange, "testPkg", "testChannel", "test.v1", replacementAndLatestKey},
			out:    out{bundle: nextBundle, key: &replacementAndLatestKey, err: nil},
		},
		{
			name:   "IgnoresLatestAtLatest",
			fields: fields{sources: sources},
			args:   args{&latestVersion, "testPkg", "testChannel", "test.v1", otherKey},
			out:    out{bundle: nil, key: nil, err: nil},
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
			got, key, err = q.FindReplacement(tt.args.currentVersion, tt.args.bundleName, tt.args.pkgName, tt.args.channelName, tt.args.initialSource)
			if err != nil {
				t.Log(err.Error())
			}
			require.Equal(t, tt.out.err, err, "%v", err)
			require.Equal(t, tt.out.bundle, got)
			require.Equal(t, tt.out.key, key)
		})
	}
}
