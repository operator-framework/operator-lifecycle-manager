package cache

import (
	"testing"

	"github.com/stretchr/testify/require"

	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

func TestGVKStringToProvidedAPISet(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want APISet
	}{
		{
			name: "EmptyString/EmptySet",
			in:   "",
			want: make(APISet),
		},
		{
			name: "Garbage/EmptySet",
			in:   ",,,,,alkjahsdfjlh!@#$%",
			want: make(APISet),
		},
		{
			name: "SingleBadGVK/EmptySet",
			in:   "this-is.not-good",
			want: make(APISet),
		},
		{
			name: "MultipleBadGVK/EmptySet",
			in:   "this-is.not-good,thisisnoteither",
			want: make(APISet),
		},
		{
			name: "SingleGoodGVK/SingleAPI",
			in:   "Goose.v1alpha1.birds.com",
			want: APISet{
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Goose"}: {},
			},
		},
		{
			name: "MutlipleGoodGVK/MultipleAPIs",
			in:   "Goose.v1alpha1.birds.com,Moose.v1alpha1.mammals.com",
			want: APISet{
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Goose"}:   {},
				opregistry.APIKey{Group: "mammals.com", Version: "v1alpha1", Kind: "Moose"}: {},
			},
		},
		{
			name: "SingleGoodGVK/SingleBadGVK/SingleAPI",
			in:   "Goose.v1alpha1.birds.com,Moose.v1alpha1",
			want: APISet{
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Goose"}: {},
			},
		},
		{
			name: "MultipleGoodGVK/MultipleBadGVK/MultipleAPIs",
			in:   "Goose.v1alpha1.birds.com,Moose.v1alpha1,Goat,Egret.v1beta1.birds.com",
			want: APISet{
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Goose"}: {},
				opregistry.APIKey{Group: "birds.com", Version: "v1beta1", Kind: "Egret"}:  {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualValues(t, tt.want, GVKStringToProvidedAPISet(tt.in))
		})
	}
}
func TestAPIKeyToGVKString(t *testing.T) {
	tests := []struct {
		name string
		in   opregistry.APIKey
		want string
	}{
		{
			name: "EmptyAPIKey",
			in:   opregistry.APIKey{},
			want: "..",
		},
		{
			name: "BadAPIKey",
			in:   opregistry.APIKey{Group: "birds. ", Version: "-"},
			want: ".-.birds. ",
		},
		{
			name: "GoodAPIKey",
			in:   opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Goose"},
			want: "Goose.v1alpha1.birds.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, APIKeyToGVKString(tt.in))
		})
	}
}

func TestAPISetString(t *testing.T) {
	tests := []struct {
		name string
		in   APISet
		want string
	}{
		{
			name: "EmptySet",
			in:   make(APISet),
			want: "",
		},
		{
			name: "OneAPI",
			in: APISet{
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Goose"}: {},
			},
			want: "Goose.v1alpha1.birds.com",
		},
		{
			name: "MutipleAPIs",
			in: APISet{
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Goose"}: {},
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Egret"}: {},
			},
			want: "Egret.v1alpha1.birds.com,Goose.v1alpha1.birds.com",
		},
		{
			name: "MutipleAPIs/OneBad",
			in: APISet{
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1"}:                {},
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Goose"}: {},
				opregistry.APIKey{Group: "birds.com", Version: "v1alpha1", Kind: "Egret"}: {},
			},
			want: ".v1alpha1.birds.com,Egret.v1alpha1.birds.com,Goose.v1alpha1.birds.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.in.String())
		})
	}
}

func TestAPISetUnion(t *testing.T) {
	type input struct {
		left  APISet
		right []APISet
	}
	tests := []struct {
		name string
		in   input
		want APISet
	}{
		{
			name: "EmptyLeft/NilRight/EmptySet",
			in: input{
				left:  make(APISet),
				right: nil,
			},
			want: make(APISet),
		},
		{
			name: "EmptyLeft/OneEmptyRight/EmptySet",
			in: input{
				left: make(APISet),
				right: []APISet{
					{},
				},
			},
			want: make(APISet),
		},
		{
			name: "EmptyLeft/OneRight/OneFromRight",
			in: input{
				left: make(APISet),
				right: []APISet{
					{
						opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
					},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
			},
		},
		{
			name: "OneLeft/EmptyRight/OneFromLeft",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: []APISet{
					{},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
			},
		},
		{
			name: "MultipleLeft/MultipleRight/AllFromLeftAndRight",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
					opregistry.APIKey{Group: "Egret", Version: "v1beta1", Kind: "birds.com"}:  {},
				},
				right: []APISet{
					{
						opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
						opregistry.APIKey{Group: "Egret", Version: "v1beta1", Kind: "birds.com"}:  {},
						opregistry.APIKey{Group: "Crow", Version: "v1beta1", Kind: "birds.com"}:   {},
					},
					{
						// Empty APISet for good measure
					},
					{
						opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
						opregistry.APIKey{Group: "Cow", Version: "v1alpha1", Kind: "mammals.com"}:   {},
						opregistry.APIKey{Group: "Egret", Version: "v1beta1", Kind: "birds.com"}:    {},
						opregistry.APIKey{Group: "Crow", Version: "v1beta1", Kind: "birds.com"}:     {},
					},
					{
						opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
						opregistry.APIKey{Group: "Cow", Version: "v1alpha1", Kind: "mammals.com"}:   {},
						opregistry.APIKey{Group: "Goat", Version: "v1beta1", Kind: "mammals.com"}:   {},
					},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
				opregistry.APIKey{Group: "Egret", Version: "v1beta1", Kind: "birds.com"}:    {},
				opregistry.APIKey{Group: "Crow", Version: "v1beta1", Kind: "birds.com"}:     {},
				opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				opregistry.APIKey{Group: "Cow", Version: "v1alpha1", Kind: "mammals.com"}:   {},
				opregistry.APIKey{Group: "Goat", Version: "v1beta1", Kind: "mammals.com"}:   {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualValues(t, tt.want, tt.in.left.Union(tt.in.right...))
		})
	}
}

func TestAPISetIntersection(t *testing.T) {
	type input struct {
		left  APISet
		right []APISet
	}
	tests := []struct {
		name string
		in   input
		want APISet
	}{
		{
			name: "EmptyLeft/NilRight/EmptySet",
			in: input{
				left:  make(APISet),
				right: nil,
			},
			want: make(APISet),
		},
		{
			name: "EmptyLeft/OneEmptyRight/EmptySet",
			in: input{
				left: make(APISet),
				right: []APISet{
					{},
				},
			},
			want: make(APISet),
		},
		{
			name: "EmptyLeft/OneRight/EmptySet",
			in: input{
				left: make(APISet),
				right: []APISet{
					{
						opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
					},
				},
			},
			want: make(APISet),
		},
		{
			name: "OneLeft/EmptyRight/NoIntersection",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: []APISet{
					{},
				},
			},
			want: make(APISet),
		},
		{
			name: "OneLeft/TwoRight/OneIntersection",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: []APISet{
					{
						opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
						opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
					},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
			},
		},
		{
			name: "OneLeft/TwoRight/SingleSet/OneIntersection",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: []APISet{
					{
						opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
						opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
					},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
			},
		},
		{
			name: "TwoLeft/OneRight/SingleSet/OneIntersection",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				},
				right: []APISet{
					{
						opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
					},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
			},
		},
		{
			name: "OneLeft/TwoRight/SeparateSets/OneIntersection",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: []APISet{
					{
						opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
					},
					{
						opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
					},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
			},
		},
		{
			name: "OneLeft/TwoRight/SeparateSets/NoIntersection",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Egret", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: []APISet{
					{
						opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
					},
					{
						opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
					},
				},
			},
			want: make(APISet),
		},
		{
			name: "MultipleLeft/MultipleRight/SeparateSets/SomeIntersection",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Egret", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Hippo", Version: "v1alpha1", Kind: "mammals.com"}: {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				},
				right: []APISet{
					{
						opregistry.APIKey{Group: "Hippo", Version: "v1alpha1", Kind: "mammals.com"}: {},
					},
					{
						opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
					},
					{
						opregistry.APIKey{Group: "Goat", Version: "v1alpha1", Kind: "mammals.com"}: {},
					},
					{
						opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
					},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Hippo", Version: "v1alpha1", Kind: "mammals.com"}: {},
				opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualValues(t, tt.want, tt.in.left.Intersection(tt.in.right...))
		})
	}
}

func TestAPISetDifference(t *testing.T) {
	type input struct {
		left  APISet
		right APISet
	}
	tests := []struct {
		name string
		in   input
		want APISet
	}{
		{
			name: "EmptySet",
			in: input{
				left:  make(APISet),
				right: make(APISet),
			},
			want: make(APISet),
		},
		{
			name: "OneLeft/EmptyRight/LeftIsDifference",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: make(APISet),
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
			},
		},
		{
			name: "EmptyLeft/OneRight/NoDifference",
			in: input{
				left: make(APISet),
				right: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
			},
			want: make(APISet),
		},
		{
			name: "OneLeft/OneRight/NoDifference",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
			},
			want: make(APISet),
		},
		{
			name: "MultipleLeft/MultipleRight/NoDifference",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				},
				right: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				},
			},
			want: make(APISet),
		},
		{
			name: "MultipleLeft/MultipleRight/SingleDifference",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
					opregistry.APIKey{Group: "Goat", Version: "v1alpha1", Kind: "mammals.com"}:  {},
				},
				right: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goat", Version: "v1alpha1", Kind: "mammals.com"}: {},
			},
		},
		{
			name: "MultipleLeft/MultipleRight/SomeDifference",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
					opregistry.APIKey{Group: "Goat", Version: "v1alpha1", Kind: "mammals.com"}:  {},
				},
				right: APISet{
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}:  {},
					opregistry.APIKey{Group: "Gopher", Version: "v1alpha2", Kind: "mammals.com"}: {},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:  {},
				opregistry.APIKey{Group: "Goat", Version: "v1alpha1", Kind: "mammals.com"}: {},
			},
		},
		{
			name: "MultipleLeft/MultipleRight/AllLeftDifference",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
					opregistry.APIKey{Group: "Goat", Version: "v1alpha1", Kind: "mammals.com"}:  {},
				},
				right: APISet{
					opregistry.APIKey{Group: "Giraffe", Version: "v1alpha1", Kind: "mammals.com"}: {},
					opregistry.APIKey{Group: "Gopher", Version: "v1alpha2", Kind: "mammals.com"}:  {},
					opregistry.APIKey{Group: "Bison", Version: "v1beta1", Kind: "mammals.com"}:    {},
				},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
				opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				opregistry.APIKey{Group: "Goat", Version: "v1alpha1", Kind: "mammals.com"}:  {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualValues(t, tt.want, tt.in.left.Difference(tt.in.right))
		})
	}
}

func TestAPISetIsSubset(t *testing.T) {
	type input struct {
		left  APISet
		right APISet
	}
	tests := []struct {
		name string
		in   input
		want bool
	}{
		{
			name: "EmptySet",
			in: input{
				left:  make(APISet),
				right: make(APISet),
			},
			want: true,
		},
		{
			name: "Same",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
			},
			want: true,
		},
		{
			name: "IsSubset",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
				right: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				},
			},
			want: true,
		},
		{
			name: "NotSubset",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				},
				right: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
				},
			},
			want: false,
		},
		{
			name: "NotSubset/EmptyRight",
			in: input{
				left: APISet{
					opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
					opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
				},
				right: make(APISet),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.in.left.IsSubset(tt.in.right))
		})
	}
}

func TestStripPlural(t *testing.T) {
	tests := []struct {
		name string
		in   APISet
		want APISet
	}{
		{
			name: "EmptySet",
			in:   make(APISet),
			want: make(APISet),
		},
		{
			name: "NilSet",
			in:   nil,
			want: make(APISet),
		},
		{
			name: "OnePluralToRemove",
			in: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com", Plural: "Geese"}: {},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}: {},
			},
		},
		{
			name: "MultiplePluralsToRemove",
			in: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com", Plural: "Geese"}:   {},
				opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com", Plural: "Moose"}: {},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
				opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
			},
		},
		{
			name: "NoPluralsToRemove",
			in: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
				opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
			},
			want: APISet{
				opregistry.APIKey{Group: "Goose", Version: "v1alpha1", Kind: "birds.com"}:   {},
				opregistry.APIKey{Group: "Moose", Version: "v1alpha1", Kind: "mammals.com"}: {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualValues(t, tt.want, tt.in.StripPlural())
		})
	}
}

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
			k := &SourceKey{
				Name:      tt.fields.Name,
				Namespace: tt.fields.Namespace,
			}
			if got := k.String(); got != tt.want {
				t.Errorf("CatalogKey.String() = %v, want %v", got, tt.want)
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
				Catalog: SourceKey{Name: tt.fields.CatalogSource, Namespace: tt.fields.CatalogSourceNamespace},
			}
			if got := i.String(); got != tt.want {
				t.Errorf("OperatorSourceInfo.String() = %v, want %v", got, tt.want)
			}
		})
	}
}
