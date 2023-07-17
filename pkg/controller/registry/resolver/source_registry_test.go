package resolver

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/blang/semver/v4"
	opver "github.com/operator-framework/api/pkg/lib/version"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewOperatorFromBundle(t *testing.T) {
	version := opver.OperatorVersion{Version: semver.MustParse("0.1.0-abc")}
	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.GroupVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testCSV",
			Namespace: "testNamespace",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces: "v1",
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    []v1alpha1.CRDDescription{},
				Required: []v1alpha1.CRDDescription{},
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned:    []v1alpha1.APIServiceDescription{},
				Required: []v1alpha1.APIServiceDescription{},
			},
			Version: version,
		},
	}

	csvJSON, err := json.Marshal(csv)
	require.NoError(t, err)
	bundleNoAPIs := &api.Bundle{
		CsvName:     "testBundle",
		PackageName: "testPackage",
		ChannelName: "testChannel",
		Version:     version.String(),
		CsvJson:     string(csvJSON),
		Object:      []string{string(csvJSON)},
	}

	csv.Spec.CustomResourceDefinitions.Owned = []v1alpha1.CRDDescription{
		{
			Name:    "owneds.crd.group.com",
			Version: "v1",
			Kind:    "OwnedCRD",
		},
	}
	csv.Spec.CustomResourceDefinitions.Required = []v1alpha1.CRDDescription{
		{
			Name:    "requireds.crd.group.com",
			Version: "v1",
			Kind:    "RequiredCRD",
		},
	}
	csv.Spec.APIServiceDefinitions.Owned = []v1alpha1.APIServiceDescription{
		{
			Name:    "ownedapis",
			Group:   "apis.group.com",
			Version: "v1",
			Kind:    "OwnedAPI",
		},
	}
	csv.Spec.APIServiceDefinitions.Required = []v1alpha1.APIServiceDescription{
		{
			Name:    "requiredapis",
			Group:   "apis.group.com",
			Version: "v1",
			Kind:    "RequiredAPI",
		},
	}

	csvJSONWithAPIs, err := json.Marshal(csv)
	require.NoError(t, err)

	crd := v1beta1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CustomResourceDefinition",
			APIVersion: "apiextensions.k8s.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "owneds.crd.group.com",
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group: "crd.group.com",
			Versions: []v1beta1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
				},
			},
			Names: v1beta1.CustomResourceDefinitionNames{
				Plural:   "owneds",
				Singular: "owned",
				Kind:     "OwnedCRD",
				ListKind: "OwnedCRDList",
			},
		},
	}
	crdJSON, err := json.Marshal(crd)
	require.NoError(t, err)

	bundleWithAPIs := &api.Bundle{
		CsvName:     "testBundle",
		PackageName: "testPackage",
		ChannelName: "testChannel",
		Version:     version.String(),
		CsvJson:     string(csvJSONWithAPIs),
		Object:      []string{string(csvJSONWithAPIs), string(crdJSON)},
		ProvidedApis: []*api.GroupVersionKind{
			{
				Group:   "crd.group.com",
				Version: "v1",
				Kind:    "OwnedCRD",
				Plural:  "owneds",
			},
			{
				Plural:  "ownedapis",
				Group:   "apis.group.com",
				Version: "v1",
				Kind:    "OwnedAPI",
			},
		},
		RequiredApis: []*api.GroupVersionKind{
			{
				Group:   "crd.group.com",
				Version: "v1",
				Kind:    "RequiredCRD",
				Plural:  "requireds",
			},
			{
				Plural:  "requiredapis",
				Group:   "apis.group.com",
				Version: "v1",
				Kind:    "RequiredAPI",
			},
		},
	}

	bundleWithPropsAndDeps := &api.Bundle{
		CsvName:     "testBundle",
		PackageName: "testPackage",
		ChannelName: "testChannel",
		Version:     version.String(),
		BundlePath:  "image",
		Properties: []*api.Property{
			{
				Type:  "olm.gvk",
				Value: "{\"group\":\"crd.group.com\",\"kind\":\"OwnedCRD\",\"version\":\"v1\"}",
			},
			{
				Type:  "olm.gvk",
				Value: "{\"group\":\"apis.group.com\",\"kind\":\"OwnedAPI\",\"version\":\"v1\"}",
			},
		},
		Dependencies: []*api.Dependency{
			{
				Type:  "olm.gvk",
				Value: "{\"group\":\"crd.group.com\",\"kind\":\"RequiredCRD\",\"version\":\"v1\"}",
			},
			{
				Type:  "olm.gvk",
				Value: "{\"group\":\"apis.group.com\",\"kind\":\"RequiredAPI\",\"version\":\"v1\"}",
			},
		},
	}

	bundleWithAPIsUnextracted := &api.Bundle{
		CsvName:     "testBundle",
		PackageName: "testPackage",
		ChannelName: "testChannel",
		CsvJson:     string(csvJSONWithAPIs),
		Object:      []string{string(csvJSONWithAPIs), string(crdJSON)},
	}

	type args struct {
		bundle         *api.Bundle
		sourceKey      cache.SourceKey
		defaultChannel string
	}
	tests := []struct {
		name    string
		args    args
		want    *cache.Entry
		wantErr error
	}{
		{
			name: "BundleNoAPIs",
			args: args{
				bundle:    bundleNoAPIs,
				sourceKey: cache.SourceKey{Name: "source", Namespace: "testNamespace"},
			},
			want: &cache.Entry{
				Name:         "testBundle",
				Version:      &version.Version,
				ProvidedAPIs: cache.EmptyAPISet(),
				RequiredAPIs: cache.EmptyAPISet(),
				Bundle:       bundleNoAPIs,
				SourceInfo: &cache.OperatorSourceInfo{
					Package: "testPackage",
					Channel: "testChannel",
					Catalog: cache.SourceKey{Name: "source", Namespace: "testNamespace"},
				},
			},
		},
		{
			name: "BundleWithAPIs",
			args: args{
				bundle:    bundleWithAPIs,
				sourceKey: cache.SourceKey{Name: "source", Namespace: "testNamespace"},
			},
			want: &cache.Entry{
				Name:    "testBundle",
				Version: &version.Version,
				ProvidedAPIs: cache.APISet{
					opregistry.APIKey{
						Group:   "crd.group.com",
						Version: "v1",
						Kind:    "OwnedCRD",
						Plural:  "owneds",
					}: struct{}{},
					opregistry.APIKey{
						Group:   "apis.group.com",
						Version: "v1",
						Kind:    "OwnedAPI",
						Plural:  "ownedapis",
					}: struct{}{},
				},
				RequiredAPIs: cache.APISet{
					opregistry.APIKey{
						Group:   "crd.group.com",
						Version: "v1",
						Kind:    "RequiredCRD",
						Plural:  "requireds",
					}: struct{}{},
					opregistry.APIKey{
						Group:   "apis.group.com",
						Version: "v1",
						Kind:    "RequiredAPI",
						Plural:  "requiredapis",
					}: struct{}{},
				},
				Properties: []*api.Property{
					{
						Type:  "olm.gvk",
						Value: "{\"group\":\"crd.group.com\",\"kind\":\"OwnedCRD\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk",
						Value: "{\"group\":\"apis.group.com\",\"kind\":\"OwnedAPI\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk.required",
						Value: "{\"group\":\"crd.group.com\",\"kind\":\"RequiredCRD\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk.required",
						Value: "{\"group\":\"apis.group.com\",\"kind\":\"RequiredAPI\",\"version\":\"v1\"}",
					},
				},
				Bundle: bundleWithAPIs,
				SourceInfo: &cache.OperatorSourceInfo{
					Package: "testPackage",
					Channel: "testChannel",
					Catalog: cache.SourceKey{Name: "source", Namespace: "testNamespace"},
				},
			},
		},
		{
			name: "BundleIgnoreCSV",
			args: args{
				bundle:    bundleWithAPIsUnextracted,
				sourceKey: cache.SourceKey{Name: "source", Namespace: "testNamespace"},
			},
			want: &cache.Entry{
				Name:         "testBundle",
				ProvidedAPIs: cache.EmptyAPISet(),
				RequiredAPIs: cache.EmptyAPISet(),
				Bundle:       bundleWithAPIsUnextracted,
				SourceInfo: &cache.OperatorSourceInfo{
					Package: "testPackage",
					Channel: "testChannel",
					Catalog: cache.SourceKey{Name: "source", Namespace: "testNamespace"},
				},
			},
		},
		{
			name: "BundleInDefaultChannel",
			args: args{
				bundle:         bundleNoAPIs,
				sourceKey:      cache.SourceKey{Name: "source", Namespace: "testNamespace"},
				defaultChannel: "testChannel",
			},
			want: &cache.Entry{
				Name:         "testBundle",
				Version:      &version.Version,
				ProvidedAPIs: cache.EmptyAPISet(),
				RequiredAPIs: cache.EmptyAPISet(),
				Bundle:       bundleNoAPIs,
				SourceInfo: &cache.OperatorSourceInfo{
					Package:        "testPackage",
					Channel:        "testChannel",
					Catalog:        cache.SourceKey{Name: "source", Namespace: "testNamespace"},
					DefaultChannel: true,
				},
			},
		},
		{
			name: "BundleWithPropertiesAndDependencies",
			args: args{
				bundle:    bundleWithPropsAndDeps,
				sourceKey: cache.SourceKey{Name: "source", Namespace: "testNamespace"},
			},
			want: &cache.Entry{
				Name:         "testBundle",
				Version:      &version.Version,
				ProvidedAPIs: cache.EmptyAPISet(),
				RequiredAPIs: cache.EmptyAPISet(),
				Properties: []*api.Property{
					{
						Type:  "olm.gvk",
						Value: "{\"group\":\"crd.group.com\",\"kind\":\"OwnedCRD\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk",
						Value: "{\"group\":\"apis.group.com\",\"kind\":\"OwnedAPI\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk.required",
						Value: "{\"group\":\"crd.group.com\",\"kind\":\"RequiredCRD\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk.required",
						Value: "{\"group\":\"apis.group.com\",\"kind\":\"RequiredAPI\",\"version\":\"v1\"}",
					},
				},
				BundlePath: bundleWithPropsAndDeps.BundlePath,
				SourceInfo: &cache.OperatorSourceInfo{
					Package: "testPackage",
					Channel: "testChannel",
					Catalog: cache.SourceKey{Name: "source", Namespace: "testNamespace"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newOperatorFromBundle(tt.args.bundle, "", tt.args.sourceKey, tt.args.defaultChannel)
			require.Equal(t, tt.wantErr, err)
			requirePropertiesEqual(t, tt.want.Properties, got.Properties)
			tt.want.Properties, got.Properties = nil, nil
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewOperatorFromBundleStripsPluralRequiredAndProvidedAPIKeys(t *testing.T) {
	key := cache.SourceKey{Namespace: "testnamespace", Name: "testname"}
	o, err := newOperatorFromBundle(&api.Bundle{
		CsvName: fmt.Sprintf("%s/%s", key.Namespace, key.Name),
		ProvidedApis: []*api.GroupVersionKind{{
			Group:   "g",
			Version: "v1",
			Kind:    "K",
			Plural:  "ks",
		}},
		RequiredApis: []*api.GroupVersionKind{{
			Group:   "g2",
			Version: "v2",
			Kind:    "K2",
			Plural:  "ks2",
		}},
	}, "", key, "")

	assert.NoError(t, err)
	assert.Equal(t, "K.v1.g", o.ProvidedAPIs.String())
	assert.Equal(t, "K2.v2.g2", o.RequiredAPIs.String())
}
