package resolver

import (
	"context"
	"fmt"

	"github.com/blang/semver"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solve"
)

type BooleanSatResolver interface {
	SolveOperators(csvs []*v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription, add map[OperatorSourceInfo]struct{}) (OperatorSet, error)
}

type SatResolver struct {
	cache OperatorCacheProvider
}

func NewDefaultSatResolver(rcp RegistryClientProvider) *SatResolver {
	return &SatResolver{
		cache: NewOperatorCache(rcp),
	}
}

type installableFilter struct {
	channel     string
	catalog     CatalogKey
	currentCSV  string
	startingCSV string
}

func (s *SatResolver) SolveOperators(namespaces []string, csvs []*v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription, add map[OperatorSourceInfo]struct{}) (OperatorSet, error) {
	var errs []error

	installables := make([]solve.Installable, 0)

	namespacedCache := s.cache.Namespaced(namespaces...)

	for _, sub := range subs {
		pkg := sub.Spec.Package
		filter := installableFilter{
			channel: sub.Spec.Channel,
			catalog: CatalogKey{
				Name:      sub.Spec.CatalogSource,
				Namespace: sub.Spec.CatalogSourceNamespace,
			},
			startingCSV: sub.Spec.StartingCSV,
			currentCSV:  sub.Status.CurrentCSV,
		}
		packageInstallables, err := s.getPackageInstallables(pkg, filter, namespacedCache)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, pkgInstallable := range packageInstallables {
			installables = append(installables, pkgInstallable)
		}
	}

	// TODO: Consider csvs not attached to subscriptions

	for opToAdd := range add {
		pkg := opToAdd.Package
		filter := installableFilter{
			startingCSV: opToAdd.StartingCSV,
			catalog:     opToAdd.Catalog,
			channel:     opToAdd.Channel,
		}

		packageInstallables, err := s.getPackageInstallables(pkg, filter, namespacedCache)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, pkgInstallable := range packageInstallables {
			installables = append(installables, pkgInstallable)
		}
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	solver, err := solve.New(solve.WithInput(installables))
	if err != nil {
		return nil, err
	}
	solvedInstallables, err := solver.Solve(context.TODO())
	if err != nil {
		return nil, err
	}

	// get the set of bundle installables from the result solved installables
	operatorInstallables := make([]BundleInstallable, 0)
	for _, installable := range solvedInstallables {
		if bundleInstallable, ok := installable.(BundleInstallable); ok {
			operatorInstallables = append(operatorInstallables, bundleInstallable)
		}
	}

	operators := make(map[string]OperatorSurface, 0)
	for _, installableOperator := range operatorInstallables {
		csvName, channel, catalog, err := installableOperator.BundleSourceInfo()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		op, err := namespacedCache.GetChannelCSVNameFromCatalog(csvName, channel, catalog)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		operators[csvName] = &op
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	return operators, nil
}

func (s *SatResolver) getPackageInstallables(pkg string, filter installableFilter, namespacedCache *NamespacedOperatorCache) (map[string]solve.Installable, error) {
	var errs []error
	installables := make(map[string]solve.Installable, 0)

	virtualInstallable := NewVirtualPackageInstallable(pkg)

	// TODO: pass the filter into the cache to use startingcsv/latestcsv as limiters
	bundles, err := namespacedCache.GetPackageChannelFromCatalog(pkg, filter.channel, filter.catalog)
	if err != nil {
		return nil, err
	}

	weightedBundles := s.getWeightedBundles(bundles, namespacedCache)

	virtDependencies := make(map[solve.Identifier]struct{}, 0)
	// add installable for each bundle version of the package
	// this is done to pin a mandatory solve to each required package
	for _, bundle := range weightedBundles {
		// traverse the dependency tree to generate the set of installables for given bundle version
		virtDependencyIdentifiers, bundleInstallables, err := s.getBundleInstallables(bundle.operator.Identifier(), filter, filter.catalog, bundle.weight, namespacedCache)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, bundleInstallable := range bundleInstallables {
			installables[string(bundleInstallable.Identifier())] = bundleInstallable
		}

		for virtDependency := range virtDependencyIdentifiers {
			virtDependencies[virtDependency] = struct{}{}
		}
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	virtualInstallable.AddDependencyFromSet(virtDependencies)
	installables[string(virtualInstallable.Identifier())] = virtualInstallable

	return installables, nil
}

func (s *SatResolver) getBundleInstallables(csvName string, filter installableFilter, preferredCatalog CatalogKey, weight int, namespacedCache *NamespacedOperatorCache) (map[solve.Identifier]struct{}, []solve.Installable, error) {
	var errs []error
	installables := make([]solve.Installable, 0)          // aggregate all of the installables at every depth
	identifiers := make(map[solve.Identifier]struct{}, 0) // keep track of depth + 1 dependencies

	bundles, err := namespacedCache.GetCsvFromAllCatalogsWithFilter(csvName, filter)
	if err != nil {
		return nil, nil, err
	}

	for _, bundle := range bundles {
		bundleSource := bundle.SourceInfo()
		if bundleSource == nil {
			err := fmt.Errorf("Unable to resolve the source of bundle %s, invalid cache", bundle.Identifier())
			errs = append(errs, err)
			continue
		}
		bundleInstallable := NewBundleInstallable(csvName, bundle.bundle.ChannelName, bundleSource.Catalog)

		for _, depVersion := range bundle.VersionDependencies() {
			depCandidates, err := namespacedCache.GetPackageVersionFromAllCatalogs(depVersion.Package, depVersion.Version)
			if err != nil {
				// If there are no candidates for a dependency, it means this bundle can't be resolved
				bundleInstallable.MakeProhibited()
				continue
			}

			bundleDependencies := make(map[solve.Identifier]struct{}, 0)
			for _, dep := range depCandidates {
				depIdentifiers, depInstallables, err := s.getBundleInstallables(dep.Identifier(), installableFilter{}, preferredCatalog, 0, namespacedCache)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				for _, depInstallable := range depInstallables {
					installables = append(installables, depInstallable)
				}
				for depIdentifier := range depIdentifiers {
					bundleDependencies[depIdentifier] = struct{}{}
				}
			}
			bundleInstallable.AddDependencyFromSet(bundleDependencies)
		}

		requiredAPIs := bundle.RequiredAPIs()
		for requiredAPI := range requiredAPIs {
			requiredAPICandidates, err := namespacedCache.GetRequiredAPIFromAllCatalogs(requiredAPI)
			if err != nil {
				// If there are no candidates for a dependency, it means this bundle can't be resolved
				bundleInstallable.MakeProhibited()
				continue
			}

			// sort requiredAPICandidates
			weightedCandidates := s.getWeightedBundles(requiredAPICandidates, namespacedCache)

			requiredAPIDependencies := make(map[solve.Identifier]struct{}, 0)
			for _, dep := range weightedCandidates {
				depIdentifiers, depInstallables, err := s.getBundleInstallables(dep.operator.Identifier(), installableFilter{}, preferredCatalog, dep.weight, namespacedCache)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				for _, depInstallable := range depInstallables {
					installables = append(installables, depInstallable)
				}
				for depIdentifier := range depIdentifiers {
					requiredAPIDependencies[depIdentifier] = struct{}{}
				}
			}
			bundleInstallable.AddDependencyFromSet(requiredAPIDependencies)
		}
		installables = append(installables, bundleInstallable)
		identifiers[bundleInstallable.Identifier()] = struct{}{}
	}

	if len(errs) > 0 {
		return nil, nil, utilerrors.NewAggregate(errs)
	}

	return identifiers, installables, nil
}

type weightedBundle struct {
	weight   int
	operator Operator
}

func (s *SatResolver) getWeightedBundles(bundles []Operator, namespacedCache *NamespacedOperatorCache) []weightedBundle {
	weightedBundles := make([]weightedBundle, 0)

	versionMap := make(map[string]Operator, 0)
	versionSlice := make([]semver.Version, 0)
	unsortableList := make([]Operator, 0)

	zeroVersion, _ := semver.Make("")

	for _, bundle := range bundles {
		version := bundle.Version() // initialized to zero value if not set in CSV
		if version.Equals(zeroVersion) {
			unsortableList = append(unsortableList, bundle)
			continue
		}

		versionMap[version.String()] = bundle
		versionSlice = append(versionSlice, *version)
	}

	semver.Sort(versionSlice)

	// todo: if len(versionSlice == 0) then try to build the graph and sort that way

	sortedBundles := make([]Operator, 0)
	for _, sortedVersion := range versionSlice {
		sortedBundles = append(sortedBundles, versionMap[sortedVersion.String()])
	}
	for _, unsortable := range unsortableList {
		sortedBundles = append(sortedBundles, unsortable)
	}

	// At the moment, the solver is bound to the highest weight of an installable
	// for sufficiently large weight. The weight is an aggregate factor that can currently
	// take in preferred catalog sources as well as version order. In order to minimize
	// that factor, multiply the total bundle version weight by the number of catalogs +
	// number of bundles to compare in order to scale minimally.
	// TODO: In the future, some more complex aggregation should be done here in order
	// to make weighting more robust.
	catalogFactor := namespacedCache.GetCacheCatalogSize() + len(sortedBundles)

	for weight, sortedBundle := range sortedBundles {
		weightedBundle := weightedBundle{
			weight:   (len(sortedBundles) - weight) * catalogFactor, // rank them highest to lowest
			operator: sortedBundle,
		}
		weightedBundles = append(weightedBundles, weightedBundle)
	}

	return weightedBundles
}
