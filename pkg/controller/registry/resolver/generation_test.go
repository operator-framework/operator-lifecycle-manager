package resolver

import (
	"fmt"
	"testing"

	"github.com/blang/semver"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

)

var NoVersion = semver.MustParse("0.0.0")

func TestNewGenerationFromCSVs(t *testing.T) {
	type args struct {
		csvs []*v1alpha1.ClusterServiceVersion
	}
	tests := []struct {
		name    string
		args    args
		want    *NamespaceGeneration
		wantErr error
	}{
		{
			name: "SingleCSV/NoProvided/NoRequired",
			args: args{
				csvs: []*v1alpha1.ClusterServiceVersion{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "operator.v1",
						},
					},
				},
			},
			want: &NamespaceGeneration{
				providedAPIs:  EmptyAPIOwnerSet(),
				requiredAPIs:  EmptyAPIMultiOwnerSet(),
				uncheckedAPIs: EmptyAPISet(),
				missingAPIs:   EmptyAPIMultiOwnerSet(),
			},
		},
		{
			name: "SingleCSV/Provided/NoRequired",
			args: args{
				csvs: []*v1alpha1.ClusterServiceVersion{
					{
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
			},
			want: &NamespaceGeneration{
				providedAPIs: map[opregistry.APIKey]OperatorSurface{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: &Operator{
						name: "operator.v1",
						providedAPIs: map[opregistry.APIKey]struct{}{
							{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
							{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
						},
						requiredAPIs: EmptyAPISet(),
						sourceInfo:   &ExistingOperator,
						version:      &NoVersion,
					},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: &Operator{
						name: "operator.v1",
						providedAPIs: map[opregistry.APIKey]struct{}{
							{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
							{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
						},
						requiredAPIs: EmptyAPISet(),
						sourceInfo:   &ExistingOperator,
						version:      &NoVersion,
					},
				},
				requiredAPIs:  EmptyAPIMultiOwnerSet(),
				uncheckedAPIs: EmptyAPISet(),
				missingAPIs:   EmptyAPIMultiOwnerSet(),
			},
		},
		{
			name: "SingleCSV/NoProvided/Required",
			args: args{
				csvs: []*v1alpha1.ClusterServiceVersion{
					{
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
			},
			want: &NamespaceGeneration{
				providedAPIs: EmptyAPIOwnerSet(),
				requiredAPIs: map[opregistry.APIKey]OperatorSet{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: map[string]OperatorSurface{
						"operator.v1": &Operator{
							name:         "operator.v1",
							providedAPIs: EmptyAPISet(),
							requiredAPIs: map[opregistry.APIKey]struct{}{
								{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
								{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
							},
							sourceInfo: &ExistingOperator,
							version:    &NoVersion,
						},
					},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: map[string]OperatorSurface{
						"operator.v1": &Operator{
							name:         "operator.v1",
							providedAPIs: EmptyAPISet(),
							requiredAPIs: map[opregistry.APIKey]struct{}{
								{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
								{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
							},
							sourceInfo: &ExistingOperator,
							version:    &NoVersion,
						},
					},
				},
				uncheckedAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
				},
				missingAPIs: map[opregistry.APIKey]OperatorSet{
					{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: map[string]OperatorSurface{
						"operator.v1": &Operator{
							name:         "operator.v1",
							providedAPIs: EmptyAPISet(),
							requiredAPIs: map[opregistry.APIKey]struct{}{
								{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
								{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
							},
							sourceInfo: &ExistingOperator,
							version:    &NoVersion,
						},
					},
					{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: map[string]OperatorSurface{
						"operator.v1": &Operator{
							name:         "operator.v1",
							providedAPIs: EmptyAPISet(),
							requiredAPIs: map[opregistry.APIKey]struct{}{
								{Group: "g", Version: "v1", Kind: "APIKind", Plural: "apikinds"}: {},
								{Group: "g", Version: "v1", Kind: "CRDKind", Plural: "crdkinds"}: {},
							},
							sourceInfo: &ExistingOperator,
							version:    &NoVersion,
						},
					},
				},
			},
		},
		{
			name: "SingleCSV/Provided/Required/Missing",
			args: args{
				csvs: []*v1alpha1.ClusterServiceVersion{
					{
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
			},
			want: &NamespaceGeneration{
				providedAPIs: map[opregistry.APIKey]OperatorSurface{
					{Group: "g", Version: "v1", Kind: "APIOwnedKind", Plural: "apiownedkinds"}: &Operator{
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
						version:    &NoVersion,
					},
					{Group: "g", Version: "v1", Kind: "CRDOwnedKind", Plural: "crdownedkinds"}: &Operator{
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
						version:    &NoVersion,
					},
				},
				requiredAPIs: map[opregistry.APIKey]OperatorSet{
					{Group: "g2", Version: "v1", Kind: "APIReqKind", Plural: "apireqkinds"}: map[string]OperatorSurface{
						"operator.v1": &Operator{
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
							version:    &NoVersion,
						},
					},
					{Group: "g2", Version: "v1", Kind: "CRDReqKind", Plural: "crdreqkinds"}: map[string]OperatorSurface{
						"operator.v1": &Operator{
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
							version:    &NoVersion,
						},
					},
				},
				uncheckedAPIs: map[opregistry.APIKey]struct{}{
					{Group: "g2", Version: "v1", Kind: "APIReqKind", Plural: "apireqkinds"}: {},
					{Group: "g2", Version: "v1", Kind: "CRDReqKind", Plural: "crdreqkinds"}: {},
				},
				missingAPIs: map[opregistry.APIKey]OperatorSet{
					{Group: "g2", Version: "v1", Kind: "APIReqKind", Plural: "apireqkinds"}: map[string]OperatorSurface{
						"operator.v1": &Operator{
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
							version:    &NoVersion,
						},
					},
					{Group: "g2", Version: "v1", Kind: "CRDReqKind", Plural: "crdreqkinds"}: map[string]OperatorSurface{
						"operator.v1": &Operator{
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
							version:    &NoVersion,
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// calculate expected operator set from input csvs
			operatorSet := EmptyOperatorSet()
			for _, csv := range tt.args.csvs {
				// there's a separate unit test for this constructor
				op, err := NewOperatorFromV1Alpha1CSV(csv)
				require.NoError(t, err)
				operatorSet[op.Identifier()] = op
			}
			tt.want.operators = operatorSet

			got, err := NewGenerationFromCluster(tt.args.csvs, nil)
			require.Equal(t, tt.wantErr, err)
			require.EqualValues(t, tt.want, got)
		})
	}
}

func TestNamespaceGeneration_AddOperator(t *testing.T) {
	type args struct {
		o OperatorSurface
	}
	tests := []struct {
		name              string
		initialOperators  []Operator
		args              args
		wantMissingAPIs   APIMultiOwnerSet
		wantUncheckedAPIs APISet
		wantErr           error
	}{
		{
			name: "APIAlreadyProvided",
			initialOperators: []Operator{
				{
					name: "existing",
					providedAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			args: args{
				o: &Operator{
					name: "new",
					providedAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			wantMissingAPIs:   EmptyAPIMultiOwnerSet(),
			wantUncheckedAPIs: EmptyAPISet(),
			wantErr:           fmt.Errorf("g/v/k (ks) already provided by existing"),
		},
		{
			name: "SatisfyWantedAPI",
			initialOperators: []Operator{
				{
					name: "existing",
					providedAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "wg", Version: "wv", Kind: "wk", Plural: "wks"}: {},
					},
				},
			},
			args: args{
				o: &Operator{
					name: "new",
					providedAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "wg", Version: "wv", Kind: "wk", Plural: "wks"}: {},
					},
				},
			},
			wantMissingAPIs:   EmptyAPIMultiOwnerSet(),
			wantUncheckedAPIs: EmptyAPISet(),
		},
		{
			name: "NewRequiredAPI",
			initialOperators: []Operator{
				{
					name: "existing",
					providedAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			args: args{
				o: &Operator{
					name: "new",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "wg", Version: "wv", Kind: "wk", Plural: "wks"}: {},
					},
				},
			},
			wantMissingAPIs: APIMultiOwnerSet{
				opregistry.APIKey{Group: "wg", Version: "wv", Kind: "wk", Plural: "wks"}: OperatorSet{
					"new": &Operator{
						name: "new",
						requiredAPIs: map[opregistry.APIKey]struct{}{
							opregistry.APIKey{Group: "wg", Version: "wv", Kind: "wk", Plural: "wks"}: {},
						},
					},
				},
			},
			wantUncheckedAPIs: APISet{opregistry.APIKey{Group: "wg", Version: "wv", Kind: "wk", Plural: "wks"}: {}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewEmptyGeneration()
			for _, o := range tt.initialOperators {
				err := g.AddOperator(&o)
				require.NoError(t, err, "expected initial operators to be valid")
			}
			err := g.AddOperator(tt.args.o)
			require.Equal(t, tt.wantErr, err)
			require.Equal(t, tt.wantMissingAPIs, g.MissingAPIs())
		})
	}
}

func TestNamespaceGeneration_RemoveOperator(t *testing.T) {
	type args struct {
		o OperatorSurface
	}
	tests := []struct {
		name              string
		initialOperators  []Operator
		args              args
		wantMissingAPIs   APIMultiOwnerSet
		wantUncheckedAPIs APISet
	}{
		{
			name: "RemoveOneOfTwoRequirers",
			initialOperators: []Operator{
				{
					name: "provider",
					providedAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
				{
					name: "requirer1",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
				{
					name: "requirer2",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			args: args{
				o: &Operator{
					name: "requirer2",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			wantMissingAPIs:   EmptyAPIMultiOwnerSet(),
			wantUncheckedAPIs: EmptyAPISet(),
		},
		{
			name: "RemoveOnlyRequirer",
			initialOperators: []Operator{
				{
					name: "provider",
					providedAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
				{
					name: "requirer",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			args: args{
				o: &Operator{
					name: "requirer",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			wantMissingAPIs:   EmptyAPIMultiOwnerSet(),
			wantUncheckedAPIs: EmptyAPISet(),
		},
		{
			name: "RemoveProvider",
			initialOperators: []Operator{
				{
					name: "provider",
					providedAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
				{
					name: "requirer",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			args: args{
				o: &Operator{
					name: "provider",
					providedAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			wantMissingAPIs: APIMultiOwnerSet{
				opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: OperatorSet{
					"requirer": &Operator{
						name: "requirer",
						requiredAPIs: map[opregistry.APIKey]struct{}{
							opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
						},
					},
				},
			},
			wantUncheckedAPIs: APISet{
				opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewEmptyGeneration()
			for _, o := range tt.initialOperators {
				err := g.AddOperator(&o)
				require.NoError(t, err, "expected initial operators to be valid")
			}
			g.RemoveOperator(tt.args.o)
			require.Equal(t, tt.wantMissingAPIs, g.MissingAPIs())
		})
	}
}

func TestNamespaceGeneration_MarkAPIChecked(t *testing.T) {
	type args struct {
		key opregistry.APIKey
	}
	tests := []struct {
		name              string
		initialOperators  []Operator
		args              args
		wantUncheckedAPIs APISet
	}{
		{
			name: "MarkRequiredAPI",
			initialOperators: []Operator{
				{
					name: "requirer",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			args:              args{key: opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}},
			wantUncheckedAPIs: EmptyAPISet(),
		},
		{
			name: "MarkOtherAPI",
			initialOperators: []Operator{
				{
					name: "requirer",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			args:              args{key: opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2", Plural: "ks2"}},
			wantUncheckedAPIs: APISet{opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewEmptyGeneration()
			for _, o := range tt.initialOperators {
				err := g.AddOperator(&o)
				require.NoError(t, err, "expected initial operators to be valid")
			}
			g.MarkAPIChecked(tt.args.key)
			require.Equal(t, tt.wantUncheckedAPIs, g.UncheckedAPIs())
		})
	}
}

func TestNamespaceGeneration_ResetUnchecked(t *testing.T) {
	type args struct {
		key opregistry.APIKey
	}
	tests := []struct {
		name              string
		initialOperators  []Operator
		args              args
		wantUncheckedAPIs APISet
	}{
		{
			name: "UncheckAfterMarkRequiredAPI",
			initialOperators: []Operator{
				{
					name: "requirer",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			args:              args{key: opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}},
			wantUncheckedAPIs: APISet{opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {}},
		},
		{
			name: "UncheckAfterMarkOtherAPI",
			initialOperators: []Operator{
				{
					name: "requirer",
					requiredAPIs: map[opregistry.APIKey]struct{}{
						opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {},
					},
				},
			},
			args:              args{key: opregistry.APIKey{Group: "g2", Version: "v2", Kind: "k2", Plural: "ks2"}},
			wantUncheckedAPIs: APISet{opregistry.APIKey{Group: "g", Version: "v", Kind: "k", Plural: "ks"}: {}},
		},
		{
			name:              "UncheckAfterNothing",
			wantUncheckedAPIs: EmptyAPISet(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewEmptyGeneration()
			for _, o := range tt.initialOperators {
				err := g.AddOperator(&o)
				require.NoError(t, err, "expected initial operators to be valid")
			}
			g.MarkAPIChecked(tt.args.key)
			g.ResetUnchecked()
			require.Equal(t, tt.wantUncheckedAPIs, g.UncheckedAPIs())
		})
	}
}
