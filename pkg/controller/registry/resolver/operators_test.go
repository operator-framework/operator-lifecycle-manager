package resolver

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

func TestCatalogKey_String(t *testing.T) {
	type fields struct {
		Name      string
		Namespace string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name:   "catalogkey",
			fields: fields{Name: "test", Namespace: "namespace"},
			want:   "test/namespace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &CatalogKey{
				Name:      tt.fields.Name,
				Namespace: tt.fields.Namespace,
			}
			if got := k.String(); got != tt.want {
				t.Errorf("CatalogKey.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIMultiOwnerSet_PopAPIKey(t *testing.T) {
	tests := []struct {
		name string
		s    APIMultiOwnerSet
		want *opregistry.APIKey
	}{
		{
			name: "Empty",
			s:    EmptyAPIMultiOwnerSet(),
			want: nil,
		},
		{
			name: "OneApi/OneOwner",
			s: map[opregistry.APIKey]OperatorSet{
				opregistry.APIKey{"g", "v", "k", "p"}: map[string]OperatorSurface{
					"owner1": &Operator{name: "op1"},
				},
			},
			want: &opregistry.APIKey{"g", "v", "k", "p"},
		},
		{
			name: "OneApi/MultiOwner",
			s: map[opregistry.APIKey]OperatorSet{
				opregistry.APIKey{"g", "v", "k", "p"}: map[string]OperatorSurface{
					"owner1": &Operator{name: "op1"},
					"owner2": &Operator{name: "op2"},
				},
			},
			want: &opregistry.APIKey{"g", "v", "k", "p"},
		},
		{
			name: "MultipleApi/MultiOwner",
			s: map[opregistry.APIKey]OperatorSet{
				opregistry.APIKey{"g", "v", "k", "p"}: map[string]OperatorSurface{
					"owner1": &Operator{name: "op1"},
					"owner2": &Operator{name: "op2"},
				},
				opregistry.APIKey{"g2", "v2", "k2", "p2"}: map[string]OperatorSurface{
					"owner1": &Operator{name: "op1"},
					"owner2": &Operator{name: "op2"},
				},
			},
			want: &opregistry.APIKey{"g", "v", "k", "p"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			startLen := len(tt.s)
			require.Equal(t, tt.s.PopAPIKey(), tt.want)

			// Verify entry removed once popped
			if tt.want != nil {
				_, ok := tt.s[*tt.want]
				require.False(t, ok)
			}

			// Verify len has decreased
			if startLen == 0 {
				require.Equal(t, 0, len(tt.s))
			} else {
				require.Equal(t, startLen-1, len(tt.s))
			}
		})
	}
}

func TestAPIMultiOwnerSet_PopAPIRequirers(t *testing.T) {
	tests := []struct {
		name string
		s    APIMultiOwnerSet
		want OperatorSet
	}{
		{
			name: "Empty",
			s:    EmptyAPIMultiOwnerSet(),
			want: nil,
		},
		{
			name: "OneApi/OneOwner",
			s: map[opregistry.APIKey]OperatorSet{
				opregistry.APIKey{"g", "v", "k", "p"}: map[string]OperatorSurface{
					"owner1": &Operator{name: "op1"},
				},
			},
			want: map[string]OperatorSurface{
				"owner1": &Operator{name: "op1"},
			},
		},
		{
			name: "OneApi/MultiOwner",
			s: map[opregistry.APIKey]OperatorSet{
				opregistry.APIKey{"g", "v", "k", "p"}: map[string]OperatorSurface{
					"owner1": &Operator{name: "op1"},
					"owner2": &Operator{name: "op2"},
				},
			},
			want: map[string]OperatorSurface{
				"owner1": &Operator{name: "op1"},
				"owner2": &Operator{name: "op2"},
			},
		},
		{
			name: "MultipleApi/MultiOwner",
			s: map[opregistry.APIKey]OperatorSet{
				opregistry.APIKey{"g", "v", "k", "p"}: map[string]OperatorSurface{
					"owner1": &Operator{name: "op1"},
					"owner2": &Operator{name: "op2"},
				},
				opregistry.APIKey{"g2", "v2", "k2", "p2"}: map[string]OperatorSurface{
					"owner1": &Operator{name: "op1"},
					"owner2": &Operator{name: "op2"},
				},
			},
			want: map[string]OperatorSurface{
				"owner1": &Operator{name: "op1"},
				"owner2": &Operator{name: "op2"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			startLen := len(tt.s)
			require.Equal(t, tt.s.PopAPIRequirers(), tt.want)

			// Verify len has decreased
			if startLen == 0 {
				require.Equal(t, 0, len(tt.s))
			} else {
				require.Equal(t, startLen-1, len(tt.s))
			}
		})
	}
}

func TestOperatorSourceInfo_String(t *testing.T) {
	type fields struct {
		Package                string
		Channel                string
		CatalogSource          string
		CatalogSourceNamespace string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "testString",
			fields: fields{
				Package:                "p",
				Channel:                "c",
				CatalogSource:          "s",
				CatalogSourceNamespace: "n",
			},
			want: "p/c in s/n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &OperatorSourceInfo{
				Package: tt.fields.Package,
				Channel: tt.fields.Channel,
				Catalog: CatalogKey{tt.fields.CatalogSource, tt.fields.CatalogSourceNamespace},
			}
			if got := i.String(); got != tt.want {
				t.Errorf("OperatorSourceInfo.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewOperatorFromBundle(t *testing.T) {
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
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    []v1alpha1.CRDDescription{},
				Required: []v1alpha1.CRDDescription{},
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned:    []v1alpha1.APIServiceDescription{},
				Required: []v1alpha1.APIServiceDescription{},
			},
		},
	}
	csvUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&csv)
	require.NoError(t, err)

	bundleNoAPIs := opregistry.NewBundle("testBundle", "testPackage", "testChannel",
		&unstructured.Unstructured{Object: csvUnst})

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

	csvUnstWithAPIs, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&csv)
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
	crdUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&crd)
	require.NoError(t, err)
	bundleWithAPIs := opregistry.NewBundle("testBundle", "testPackage", "testChannel",
		&unstructured.Unstructured{Object: csvUnstWithAPIs}, &unstructured.Unstructured{Object: crdUnst})

	type args struct {
		bundle    *opregistry.Bundle
		sourceKey CatalogKey
	}
	tests := []struct {
		name    string
		args    args
		want    *Operator
		wantErr error
	}{
		{
			name: "BundleNoAPIs",
			args: args{
				bundle:    bundleNoAPIs,
				sourceKey: CatalogKey{Name: "source", Namespace: "testNamespace"},
			},
			want: &Operator{
				name:         "testCSV",
				providedAPIs: EmptyAPISet(),
				requiredAPIs: EmptyAPISet(),
				bundle:       bundleNoAPIs,
				sourceInfo: &OperatorSourceInfo{
					Package: "testPackage",
					Channel: "testChannel",
					Catalog: CatalogKey{"source", "testNamespace"},
				},
			},
		},
		{
			name: "BundleWithAPIs",
			args: args{
				bundle:    bundleWithAPIs,
				sourceKey: CatalogKey{Name: "source", Namespace: "testNamespace"},
			},
			want: &Operator{
				name: "testCSV",
				providedAPIs: APISet{
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
				requiredAPIs: APISet{
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
				bundle: bundleWithAPIs,
				sourceInfo: &OperatorSourceInfo{
					Package: "testPackage",
					Channel: "testChannel",
					Catalog: CatalogKey{"source", "testNamespace"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewOperatorFromBundle(tt.args.bundle, tt.args.sourceKey)
			require.Equal(t, tt.wantErr, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewOperatorFromCSV(t *testing.T) {
	type args struct {
		csv *v1alpha1.ClusterServiceVersion
	}
	tests := []struct {
		name    string
		args    args
		want    *Operator
		wantErr error
	}{
		{
			name: "NoProvided/NoRequired",
			args: args{
				csv: &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "operator.v1",
					},
				},
			},
			want: &Operator{
				name:         "operator.v1",
				providedAPIs: EmptyAPISet(),
				requiredAPIs: EmptyAPISet(),
				sourceInfo:   &ExistingOperator,
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
			want: &Operator{
				name: "operator.v1",
				providedAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
				},
				requiredAPIs: EmptyAPISet(),
				sourceInfo:   &ExistingOperator,
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
			want: &Operator{
				name:         "operator.v1",
				providedAPIs: EmptyAPISet(),
				requiredAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
				},
				sourceInfo: &ExistingOperator,
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
			want: &Operator{
				name: "operator.v1",
				providedAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g", Version: "v1", Kind: "APIOwnedKind", Plural: "apiownedkinds"}: {},
					{Group: "g", Version: "v1", Kind: "CRDOwnedKind", Plural: "crdownedkinds"}: {},
				},
				requiredAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g2", Version: "v1", Kind: "APIReqKind", Plural: "apireqkinds"}: {},
					{Group: "g2", Version: "v1", Kind: "CRDReqKind", Plural: "crdreqkinds"}: {},
				},
				sourceInfo: &ExistingOperator,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewOperatorFromCSV(tt.args.csv)
			require.Equal(t, tt.wantErr, err)
			require.Equal(t, tt.want, got)
		})
	}
}
