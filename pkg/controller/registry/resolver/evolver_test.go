package resolver

import (
	"fmt"
	"testing"

	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/stretchr/testify/require"
)

func TestNamespaceGenerationEvolver(t *testing.T) {
	type fields struct {
		querier SourceQuerier
		gen     Generation
	}
	type args struct {
		add map[OperatorSourceInfo]struct{}
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr error
		wantGen Generation
	}{
		{
			name: "NotQueryable",
			fields: fields{
				querier: NewFakeSourceQuerier(nil),
				gen:     NewEmptyGeneration(),
			},
			args:    args{nil},
			wantErr: fmt.Errorf("no catalog sources available"),
			wantGen: NewEmptyGeneration(),
		},
		{
			name: "NoRequiredAPIs",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("csv1", "p", "c", "", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewEmptyGeneration(),
			},
			wantGen: NewEmptyGeneration(),
		},
		{
			name: "NoNewRequiredAPIs",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("csv1", "p", "c", "", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
						bundle("nothing.v1", "nothing", "channel", "", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("op1", "pkgA", "c", "", "s", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
				),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package: "nothing",
						Channel: "channel",
						Catalog: CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("op1", "pkgA", "c", "", "s", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
				NewFakeOperatorSurface("nothing.v1", "nothing", "channel", "", "catsrc", "", nil, nil, nil, nil, nil),
			),
		},
		{
			name: "NoNewRequiredAPIs/StartingCSV",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("csv1", "p", "c", "", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
						bundle("nothing.v1", "nothing", "channel", "", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
						bundle("nothing.v2", "nothing", "channel", "nothing.v1", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("op1", "pkgA", "c", "", "s", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
				),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package:     "nothing",
						Channel:     "channel",
						StartingCSV: "nothing.v1",
						Catalog:     CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("op1", "pkgA", "c", "", "s", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
				NewFakeOperatorSurface("nothing.v1", "nothing", "channel", "", "catsrc", "nothing.v1", nil, nil, nil, nil, nil),
			),
		},
		{
			name: "NoNewRequiredAPIs/StartingCSV/NotFound",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("csv1", "p", "c", "", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
						bundle("nothing.v2", "nothing", "channel", "", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("op1", "pkgA", "c", "", "s", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
				),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package:     "nothing",
						Channel:     "channel",
						StartingCSV: "nothing.v1",
						Catalog:     CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("op1", "pkgA", "c", "", "s", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
			),
			wantErr: fmt.Errorf("{nothing channel nothing.v1 {catsrc catsrc-namespace}} not found: no bundle found"),
		},
		{
			// the incoming subscription requires apis that can't be found
			// this should contract back to the original set
			name: "NewRequiredAPIs/NoProviderFound",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("depender.v1", "depender", "channel", "", EmptyAPISet(), APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewEmptyGeneration(),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package: "depender",
						Channel: "channel",
						Catalog: CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewEmptyGeneration(),
		},
		{
			// the incoming subscription requires apis that can't be found
			// this should contract back to the original set
			name: "NewRequiredAPIs/NoProviderFound/NonEmptyStartingGeneration",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("depender.v1", "depender", "channel", "", EmptyAPISet(), APISet{opregistry.APIKey{"g2", "v2", "k2", "k2s"}: {}}, EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("op1", "pkgA", "c", "", "s", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
				),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package: "depender",
						Channel: "channel",
						Catalog: CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("op1", "pkgA", "c", "", "s", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
			),
		},
		{
			// the incoming subscription requires apis that can be found
			// this should produce a set with the new provider
			name: "NewRequiredAPIs/FoundProvider",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("depender.v1", "depender", "channel", "", EmptyAPISet(), APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet()),
						bundle("provider.v1", "provider", "channel", "", APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewEmptyGeneration(),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package: "depender",
						Channel: "channel",
						Catalog: CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("depender.v1", "depender", "channel", "", "catsrc", "", nil, []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil),
				NewFakeOperatorSurface("provider.v1", "provider", "channel", "", "catsrc", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
			),
		},
		{
			// the incoming subscription requires apis that can be found
			// but the provider subscription also requires apis that can't be found
			// this should contract back to the original set
			name: "NewRequiredAPIs/FoundProvider/ProviderRequired/NoSecondaryProvider",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("depender.v1", "depender", "channel", "", EmptyAPISet(),
							APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet()),
						bundle("provider.v1", "provider", "channel", "",
							APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}},
							APISet{opregistry.APIKey{"g2", "v2", "k2", "k2s"}: {}}, EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewEmptyGeneration(),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package: "depender",
						Channel: "channel",
						Catalog: CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewEmptyGeneration(),
		},
		{
			// the incoming subscription requires apis that can be found
			// and the provider also requires apis that can be found
			// this should produce a set with three new providers
			name: "NewRequiredAPIs/FoundProvider/ProviderRequired/SecondaryProviderFound",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("depender.v1", "depender", "channel", "", EmptyAPISet(),
							APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet()),
						bundle("provider.v1", "provider", "channel", "",
							APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}},
							APISet{opregistry.APIKey{"g2", "v2", "k2", "k2s"}: {}}, EmptyAPISet(), EmptyAPISet()),
						bundle("provider2.v1", "provider2", "channel", "",
							APISet{opregistry.APIKey{"g2", "v2", "k2", "k2s"}: {}},
							EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewEmptyGeneration(),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package: "depender",
						Channel: "channel",
						Catalog: CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("depender.v1", "depender", "channel", "", "catsrc", "", nil, []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil),
				NewFakeOperatorSurface("provider.v1", "provider", "channel", "", "catsrc", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, []opregistry.APIKey{{"g2", "v2", "k2", "k2s"}}, nil, nil, nil),
				NewFakeOperatorSurface("provider2.v1", "provider2", "channel", "", "catsrc", "", []opregistry.APIKey{{"g2", "v2", "k2", "k2s"}}, nil, nil, nil, nil),
			),
		},
		{
			// the incoming subscription requires apis that can be found
			// and the provider also requires apis that can be found
			// this should produce a set with three new providers
			// tests dependency between crd and apiservice provided apis as a sanity check - evolver shouldn't care
			name: "NewRequiredCRDAPIs/FoundCRDProvider/ProviderAPIRequired/SecondaryAPIProviderFound",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("depender.v1", "depender", "channel", "", EmptyAPISet(),
							APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet()),
						bundle("provider.v1", "provider", "channel", "",
							APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(),
							EmptyAPISet(), APISet{opregistry.APIKey{"g2", "v2", "k2", "k2s"}: {}}),
						bundle("provider2.v1", "provider2", "channel", "",
							EmptyAPISet(), EmptyAPISet(),
							APISet{opregistry.APIKey{"g2", "v2", "k2", "k2s"}: {}}, EmptyAPISet()),
					},
				}),
				gen: NewEmptyGeneration(),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package: "depender",
						Channel: "channel",
						Catalog: CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("depender.v1", "depender", "channel", "", "catsrc", "", nil, []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil),
				NewFakeOperatorSurface("provider.v1", "provider", "channel", "", "catsrc", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, []opregistry.APIKey{{"g2", "v2", "k2", "k2s"}}, nil),
				NewFakeOperatorSurface("provider2.v1", "provider2", "channel", "", "catsrc", "", nil, nil, []opregistry.APIKey{{"g2", "v2", "k2", "k2s"}}, nil, nil),
			),
		},
		{
			name: "NewRequiredAPIs/FoundProvider/ProviderRequired/SecondaryProviderFound/RequiresAlreadyProvidedAPIs",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("depender.v1", "depender", "channel", "",
							EmptyAPISet(),
							APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet()),
						bundle("provider.v1", "provider", "channel", "",
							APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}},
							APISet{opregistry.APIKey{"g2", "v2", "k2", "k2s"}: {}}, EmptyAPISet(), EmptyAPISet()),
						bundle("provider2.v1", "provider2", "channel", "",
							APISet{opregistry.APIKey{"g2", "v2", "k2", "k2s"}: {}},
							APISet{opregistry.APIKey{"g3", "v3", "k3", "k3s"}: {}}, EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("original", "o", "c", "", "s", "", []opregistry.APIKey{{"g3", "v3", "k3", "k3s"}}, nil, nil, nil, nil),
				),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package: "depender",
						Channel: "channel",
						Catalog: CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("original", "o", "c", "", "s", "", []opregistry.APIKey{{"g3", "v3", "k3", "k3s"}}, nil, nil, nil, nil),
				NewFakeOperatorSurface("depender.v1", "depender", "channel", "", "catsrc", "", nil, []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil),
				NewFakeOperatorSurface("provider.v1", "provider", "channel", "", "catsrc", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, []opregistry.APIKey{{"g2", "v2", "k2", "k2s"}}, nil, nil, nil),
				NewFakeOperatorSurface("provider2.v1", "provider2", "channel", "", "catsrc", "", []opregistry.APIKey{{"g2", "v2", "k2", "k2s"}}, []opregistry.APIKey{{"g3", "v3", "k3", "k3s"}}, nil, nil, nil),
			),
		},
		{
			// the incoming subscription requires apis that can be found
			// and an existing operator has an update
			// this should produce a set with the new provider
			name: "UpdateRequired/NewRequiredAPIs/FoundProvider",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("depender.v1", "depender", "channel", "", EmptyAPISet(), APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet()),
						bundle("provider.v1", "provider", "channel", "", APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
						bundle("updated", "o", "c", "original", nil, nil, nil, nil),
					},
				}),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("original", "o", "c", "", "catsrc", "", nil, nil, nil, nil, nil),
				),
			},
			args: args{
				add: map[OperatorSourceInfo]struct{}{
					OperatorSourceInfo{
						Package: "depender",
						Channel: "channel",
						Catalog: CatalogKey{"catsrc", "catsrc-namespace"},
					}: {},
				},
			},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("updated", "o", "c", "original", "catsrc", "", nil, nil, nil, nil, nil),
				NewFakeOperatorSurface("depender.v1", "depender", "channel", "", "catsrc", "", nil, []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil),
				NewFakeOperatorSurface("provider.v1", "provider", "channel", "", "catsrc", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
			),
		},
		{
			// an existing operator has multiple updates available
			// a single evolution should update to next, not latest
			name: "UpdateRequired/MultipleUpdates",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("updated", "o", "c", "original", nil, nil, nil, nil),
						bundle("updated.v2", "o", "c", "updated", nil, nil, nil, nil),
						bundle("updated.v3", "o", "c", "updated.v2", nil, nil, nil, nil),
					},
				}),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("original", "o", "c", "", "catsrc", "", nil, nil, nil, nil, nil),
				),
			},
			args: args{},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("updated", "o", "c", "original", "catsrc", "", nil, nil, nil, nil, nil),
			),
		},
		{
			// an existing operator has an update available and skips previous versions via channel head annotations
			name: "UpdateRequired/SkipVersions",
			fields: fields{
				querier: NewFakeSourceQuerierCustomReplacement(CatalogKey{"catsrc", "catsrc-namespace"}, bundle("updated.v3", "o", "c", "updated.v2", nil, nil, nil, nil)),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("original", "o", "c", "", "catsrc", "", nil, nil, nil, nil, nil),
				),
			},
			args: args{},
			wantGen: NewGenerationFromOperators(
				// the csv in the bundle still has the original replaces field, but the surface has the value overridden
				withReplaces(NewFakeOperatorSurface("updated.v3", "o", "c", "updated.v2", "catsrc", "", nil, nil, nil, nil, nil),
					"original"),
			),
		},
		{
			name: "OwnershipTransfer",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("depender.v2", "depender", "channel", "depender.v1",
							APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}}, EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
						bundle("original.v2", "o", "c", "original", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("original", "o", "c", "", "catsrc", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
					NewFakeOperatorSurface("depender.v1", "depender", "channel", "", "catsrc", "", nil, []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil),
				),
			},
			args: args{},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("original.v2", "o", "c", "original", "catsrc", "", nil, nil, nil, nil, nil),
				NewFakeOperatorSurface("depender.v2", "depender", "channel", "depender.v1", "catsrc", "", []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil, nil),
			),
		},
		{
			name: "PicksOlderProvider",
			fields: fields{
				querier: NewFakeSourceQuerier(map[CatalogKey][]*api.Bundle{
					CatalogKey{"catsrc", "catsrc-namespace"}: {
						bundle("original", "o", "c", "", APISet{opregistry.APIKey{"g", "v", "k", "ks"}: {}},  EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
						bundle("original.v2", "o", "c", "original", EmptyAPISet(), EmptyAPISet(), EmptyAPISet(), EmptyAPISet()),
					},
				}),
				gen: NewGenerationFromOperators(
					NewFakeOperatorSurface("depender.v1", "depender", "channel", "", "catsrc", "", nil, []opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil),
				),
			},
			args: args{},
			wantGen: NewGenerationFromOperators(
				NewFakeOperatorSurface("original", "o", "c", "", "catsrc", "",  []opregistry.APIKey{{"g", "v", "k", "ks"}},nil, nil, nil, nil),
				NewFakeOperatorSurface("depender.v1", "depender", "channel", "", "catsrc", "", nil,[]opregistry.APIKey{{"g", "v", "k", "ks"}}, nil, nil, nil),
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewNamespaceGenerationEvolver(tt.fields.querier, tt.fields.gen)
			err := e.Evolve(tt.args.add)
			if tt.wantErr != nil {
				require.EqualError(t, tt.wantErr, err.Error())
			} else {
				// if there was no error, then the generation should have "evolved" to a new good set
				require.EqualValues(t, EmptyAPIMultiOwnerSet(), tt.fields.gen.MissingAPIs())
			}
			require.EqualValues(t, tt.wantGen, tt.fields.gen)
		})
	}
}
