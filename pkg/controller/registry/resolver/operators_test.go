package resolver

import (
	"encoding/json"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opver "github.com/operator-framework/api/pkg/lib/version"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/api"
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
	}{
		{
			name: "Empty",
			s:    EmptyAPIMultiOwnerSet(),
		},
		{
			name: "OneApi/OneOwner",
			s: map[opregistry.APIKey]OperatorSet{
				opregistry.APIKey{"g", "v", "k", "p"}: map[string]OperatorSurface{
					"owner1": &Operator{name: "op1"},
				},
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
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			startLen := len(tt.s)

			popped := tt.s.PopAPIKey()

			if startLen == 0 {
				require.Nil(t, popped, "popped key from empty MultiOwnerSet should be nil")
				require.Equal(t, 0, len(tt.s))
			} else {
				_, found := tt.s[*popped]
				require.False(t, found, "popped key should not still exist in set")
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
	version := opver.OperatorVersion{semver.MustParse("0.1.0-abc")}
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

	csvJson, err := json.Marshal(csv)
	require.NoError(t, err)
	bundleNoAPIs := &api.Bundle{
		CsvName:     "testBundle",
		PackageName: "testPackage",
		ChannelName: "testChannel",
		Version:     version.String(),
		CsvJson:     string(csvJson),
		Object:      []string{string(csvJson)},
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

	csvJsonWithApis, err := json.Marshal(csv)
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
	crdJson, err := json.Marshal(crd)
	require.NoError(t, err)

	bundleWithAPIs := &api.Bundle{
		CsvName:     "testBundle",
		PackageName: "testPackage",
		ChannelName: "testChannel",
		Version:     version.String(),
		CsvJson:     string(csvJsonWithApis),
		Object:      []string{string(csvJsonWithApis), string(crdJson)},
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

	bundleWithAPIsUnextracted := &api.Bundle{
		CsvName:     "testBundle",
		PackageName: "testPackage",
		ChannelName: "testChannel",
		CsvJson:     string(csvJsonWithApis),
		Object:      []string{string(csvJsonWithApis), string(crdJson)},
	}

	type args struct {
		bundle    *api.Bundle
		sourceKey CatalogKey
		replaces  string
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
				// lack of full api response falls back to csv name
				name:         "testCSV",
				version:      &version.Version,
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
				name:    "testBundle",
				version: &version.Version,
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
		{
			name: "BundleReplaceOverrides",
			args: args{
				bundle:    bundleNoAPIs,
				sourceKey: CatalogKey{Name: "source", Namespace: "testNamespace"},
			},
			want: &Operator{
				// lack of full api response falls back to csv name
				name:         "testCSV",
				providedAPIs: EmptyAPISet(),
				requiredAPIs: EmptyAPISet(),
				bundle:       bundleNoAPIs,
				version:      &version.Version,
				sourceInfo: &OperatorSourceInfo{
					Package: "testPackage",
					Channel: "testChannel",
					Catalog: CatalogKey{"source", "testNamespace"},
				},
			},
		},
		{
			name: "BundleCsvFallback",
			args: args{
				bundle:    bundleWithAPIsUnextracted,
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
				bundle:  bundleWithAPIsUnextracted,
				version: &version.Version,
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
			got, err := NewOperatorFromBundle(tt.args.bundle, "", tt.args.sourceKey)
			require.Equal(t, tt.wantErr, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewOperatorFromCSV(t *testing.T) {
	version := opver.OperatorVersion{semver.MustParse("0.1.0-abc")}
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
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Version: version,
					},
				},
			},
			want: &Operator{
				name:         "operator.v1",
				providedAPIs: EmptyAPISet(),
				requiredAPIs: EmptyAPISet(),
				sourceInfo:   &ExistingOperator,
				version:      &version.Version,
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
			want: &Operator{
				name: "operator.v1",
				providedAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
				},
				requiredAPIs: EmptyAPISet(),
				sourceInfo:   &ExistingOperator,
				version:      &version.Version,
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
			want: &Operator{
				name:         "operator.v1",
				providedAPIs: EmptyAPISet(),
				requiredAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
				},
				sourceInfo: &ExistingOperator,
				version:    &version.Version,
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
				version:    &version.Version,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewOperatorFromV1Alpha1CSV(tt.args.csv)
			require.Equal(t, tt.wantErr, err)
			require.Equal(t, tt.want, got)
		})
	}
}
