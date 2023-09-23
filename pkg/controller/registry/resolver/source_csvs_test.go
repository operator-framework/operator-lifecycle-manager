package resolver

import (
	"context"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	opver "github.com/operator-framework/api/pkg/lib/version"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

func TestInferProperties(t *testing.T) {
	catalog := cache.SourceKey{Namespace: "namespace", Name: "name"}

	for _, tc := range []struct {
		Name          string
		CSV           *v1alpha1.ClusterServiceVersion
		Subscriptions []*v1alpha1.Subscription
		Expected      []*api.Property
	}{
		{
			Name: "no subscriptions infers no properties",
			CSV: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "a",
				},
			},
		},
		{
			Name: "one unrelated subscription infers no properties",
			CSV: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "a",
				},
			},
			Subscriptions: []*v1alpha1.Subscription{
				{
					Spec: &v1alpha1.SubscriptionSpec{
						Package: "x",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstalledCSV: "b",
					},
				},
			},
		},
		{
			Name: "one subscription with empty package field infers no properties",
			CSV: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "a",
				},
			},
			Subscriptions: []*v1alpha1.Subscription{
				{
					Spec: &v1alpha1.SubscriptionSpec{
						Package: "",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstalledCSV: "a",
					},
				},
			},
		},
		{
			Name: "two related subscriptions infers no properties",
			CSV: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "a",
				},
			},
			Subscriptions: []*v1alpha1.Subscription{
				{
					Spec: &v1alpha1.SubscriptionSpec{
						Package: "x",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstalledCSV: "a",
					},
				},
				{
					Spec: &v1alpha1.SubscriptionSpec{
						Package: "y",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstalledCSV: "a",
					},
				},
			},
		},
		{
			Name: "one matching subscription infers package property",
			CSV: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "a",
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					Version: opver.OperatorVersion{Version: semver.MustParse("1.2.3")},
				},
			},
			Subscriptions: []*v1alpha1.Subscription{
				{
					Spec: &v1alpha1.SubscriptionSpec{
						Package:                "x",
						CatalogSource:          catalog.Name,
						CatalogSourceNamespace: catalog.Namespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						InstalledCSV: "a",
					},
				},
			},
			Expected: []*api.Property{
				{
					Type:  "olm.package",
					Value: `{"packageName":"x","version":"1.2.3"}`,
				},
			},
		},
		{
			Name: "one matching subscription to other-namespace catalogsource infers package property",
			CSV: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "a",
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					Version: opver.OperatorVersion{Version: semver.MustParse("1.2.3")},
				},
			},
			Subscriptions: []*v1alpha1.Subscription{
				{
					Spec: &v1alpha1.SubscriptionSpec{
						Package:                "x",
						CatalogSource:          "other-name",
						CatalogSourceNamespace: "other-namespace",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstalledCSV: "a",
					},
				},
			},
			Expected: []*api.Property{
				{
					Type:  "olm.package",
					Value: `{"packageName":"x","version":"1.2.3"}`,
				},
			},
		},
		{
			Name: "one matching subscription infers package property without csv version",
			CSV: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "a",
				},
			},
			Subscriptions: []*v1alpha1.Subscription{
				{
					Spec: &v1alpha1.SubscriptionSpec{
						Package:                "x",
						CatalogSource:          catalog.Name,
						CatalogSourceNamespace: catalog.Namespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						InstalledCSV: "a",
					},
				},
			},
			Expected: []*api.Property{
				{
					Type:  "olm.package",
					Value: `{"packageName":"x","version":""}`,
				},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			require := require.New(t)
			logger, _ := test.NewNullLogger()
			s := &csvSource{
				logger: logger,
			}
			actual, err := s.inferProperties(tc.CSV, tc.Subscriptions)
			require.NoError(err)
			require.Equal(tc.Expected, actual)
		})
	}
}

func TestNewEntryFromCSV(t *testing.T) {
	version := opver.OperatorVersion{Version: semver.MustParse("0.1.0-abc")}
	type args struct {
		csv *v1alpha1.ClusterServiceVersion
	}
	tests := []struct {
		name    string
		args    args
		want    *cache.Entry
		wantErr error
	}{
		{
			name: "NoProvided/NoRequired",
			args: args{
				csv: &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "operator.v1",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Version: version,
					},
				},
			},
			want: &cache.Entry{
				Name:         "operator.v1",
				ProvidedAPIs: cache.EmptyAPISet(),
				RequiredAPIs: cache.EmptyAPISet(),
				SourceInfo:   &cache.OperatorSourceInfo{},
				Version:      &version.Version,
			},
		},
		{
			name: "Provided/NoRequired",
			args: args{
				csv: &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "operator.v1",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Version: version,
						CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
							Owned: []v1alpha1.CRDDescription{
								{
									Name:    "crdkinds.g",
									Version: "v1",
									Kind:    "CRDKind",
								},
							},
						},
						APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
							Owned: []v1alpha1.APIServiceDescription{
								{
									Name:    "apikinds",
									Group:   "g",
									Version: "v1",
									Kind:    "APIKind",
								},
							},
						},
					},
				},
			},
			want: &cache.Entry{
				Name: "operator.v1",
				ProvidedAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
				},
				Properties: []*api.Property{
					{
						Type:  "olm.gvk",
						Value: "{\"group\":\"g\",\"kind\":\"APIKind\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk",
						Value: "{\"group\":\"g\",\"kind\":\"CRDKind\",\"version\":\"v1\"}",
					},
				},
				RequiredAPIs: cache.EmptyAPISet(),
				SourceInfo:   &cache.OperatorSourceInfo{},
				Version:      &version.Version,
			},
		},
		{
			name: "NoProvided/Required",
			args: args{
				csv: &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "operator.v1",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Version: version,
						CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
							Required: []v1alpha1.CRDDescription{
								{
									Name:    "crdkinds.g",
									Version: "v1",
									Kind:    "CRDKind",
								},
							},
						},
						APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
							Required: []v1alpha1.APIServiceDescription{
								{
									Name:    "apikinds",
									Group:   "g",
									Version: "v1",
									Kind:    "APIKind",
								},
							},
						},
					},
				},
			},
			want: &cache.Entry{
				Name:         "operator.v1",
				ProvidedAPIs: cache.EmptyAPISet(),
				RequiredAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
				},
				Properties: []*api.Property{
					{
						Type:  "olm.gvk.required",
						Value: "{\"group\":\"g\",\"kind\":\"APIKind\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk.required",
						Value: "{\"group\":\"g\",\"kind\":\"CRDKind\",\"version\":\"v1\"}",
					},
				},
				SourceInfo: &cache.OperatorSourceInfo{},
				Version:    &version.Version,
			},
		},
		{
			name: "Provided/Required",
			args: args{
				csv: &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "operator.v1",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Version: version,
						CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
							Owned: []v1alpha1.CRDDescription{
								{
									Name:    "crdownedkinds.g",
									Version: "v1",
									Kind:    "CRDOwnedKind",
								},
							},
							Required: []v1alpha1.CRDDescription{
								{
									Name:    "crdreqkinds.g2",
									Version: "v1",
									Kind:    "CRDReqKind",
								},
							},
						},
						APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
							Owned: []v1alpha1.APIServiceDescription{
								{
									Name:    "apiownedkinds",
									Group:   "g",
									Version: "v1",
									Kind:    "APIOwnedKind",
								},
							},
							Required: []v1alpha1.APIServiceDescription{
								{
									Name:    "apireqkinds",
									Group:   "g2",
									Version: "v1",
									Kind:    "APIReqKind",
								},
							},
						},
					},
				},
			},
			want: &cache.Entry{
				Name: "operator.v1",
				ProvidedAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g", Version: "v1", Kind: "APIOwnedKind", Plural: "apiownedkinds"}: {},
					{Group: "g", Version: "v1", Kind: "CRDOwnedKind", Plural: "crdownedkinds"}: {},
				},
				RequiredAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g2", Version: "v1", Kind: "APIReqKind", Plural: "apireqkinds"}: {},
					{Group: "g2", Version: "v1", Kind: "CRDReqKind", Plural: "crdreqkinds"}: {},
				},
				Properties: []*api.Property{
					{
						Type:  "olm.gvk",
						Value: "{\"group\":\"g\",\"kind\":\"APIOwnedKind\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk",
						Value: "{\"group\":\"g\",\"kind\":\"CRDOwnedKind\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk.required",
						Value: "{\"group\":\"g2\",\"kind\":\"APIReqKind\",\"version\":\"v1\"}",
					},
					{
						Type:  "olm.gvk.required",
						Value: "{\"group\":\"g2\",\"kind\":\"CRDReqKind\",\"version\":\"v1\"}",
					},
				},
				SourceInfo: &cache.OperatorSourceInfo{},
				Version:    &version.Version,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newEntryFromV1Alpha1CSV(tt.args.csv)
			require.Equal(t, tt.wantErr, err)
			requirePropertiesEqual(t, tt.want.Properties, got.Properties)
			tt.want.Properties, got.Properties = nil, nil
			require.Equal(t, tt.want, got)
		})
	}
}

type fakeCSVLister []*v1alpha1.ClusterServiceVersion

func (f fakeCSVLister) List(selector labels.Selector) ([]*v1alpha1.ClusterServiceVersion, error) {
	return f, nil
}

func (f fakeCSVLister) Get(name string) (*v1alpha1.ClusterServiceVersion, error) {
	for _, csv := range f {
		if csv.Name == name {
			return csv, nil
		}
	}
	return nil, errors.NewNotFound(v1alpha1.SchemeGroupVersion.WithResource("clusterserviceversions").GroupResource(), name)
}

type fakeSubscriptionLister []*v1alpha1.Subscription

func (f fakeSubscriptionLister) List(selector labels.Selector) ([]*v1alpha1.Subscription, error) {
	return f, nil
}

func (f fakeSubscriptionLister) Get(name string) (*v1alpha1.Subscription, error) {
	for _, sub := range f {
		if sub.Name == name {
			return sub, nil
		}
	}
	return nil, errors.NewNotFound(v1alpha1.SchemeGroupVersion.WithResource("subscriptions").GroupResource(), name)
}

type fakeOperatorGroupLister []*operatorsv1.OperatorGroup

func (f fakeOperatorGroupLister) List(selector labels.Selector) ([]*operatorsv1.OperatorGroup, error) {
	return f, nil
}

func (f fakeOperatorGroupLister) Get(name string) (*operatorsv1.OperatorGroup, error) {
	for _, og := range f {
		if og.Name == name {
			return og, nil
		}
	}
	return nil, errors.NewNotFound(operatorsv1.SchemeGroupVersion.WithResource("operatorgroups").GroupResource(), name)
}

func TestPropertiesAnnotationHonored(t *testing.T) {
	og := &operatorsv1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "og",
			Namespace: "fake-ns",
		},
	}
	src := &csvSource{
		csvLister: fakeCSVLister{
			&v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fake-ns",
					Name:      "csv",
					Annotations: map[string]string{
						"operatorframework.io/properties": `{"properties":[{"type":"test-type","value":{"test":"value"}}]}`,
					},
				},
			},
		},
		subLister: fakeSubscriptionLister{&v1alpha1.Subscription{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "fake-ns",
				Name:      "sub",
			},
			Status: v1alpha1.SubscriptionStatus{
				InstalledCSV: "csv",
			},
		}},
		ogLister: fakeOperatorGroupLister{og},
	}
	ss, err := src.Snapshot(context.Background())
	require.NoError(t, err)
	requirePropertiesEqual(t, []*api.Property{{Type: "test-type", Value: `{"test":"value"}`}}, ss.Entries[0].Properties)
}
