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
	visited := make(map[OperatorSurface]*BundleInstallable, 0)

	namespacedCache := s.cache.Namespaced(namespaces...)

	// build constraints for existing installed Subscriptions
	for _, sub := range subs {
		if sub.Status.InstalledCSV == "" {
			continue
		}
		pkg := sub.Spec.Package
		catalog := CatalogKey{
			Name:      sub.Spec.CatalogSource,
			Namespace: sub.Spec.CatalogSourceNamespace,
		}
		predicates := []OperatorPredicate{InChannel(pkg, sub.Spec.Channel)}
		var current *Operator
		for _, csv := range csvs {
			if csv.Name == sub.Status.InstalledCSV {
				op, err := NewOperatorFromV1Alpha1CSV(csv)
				if err != nil {
					return nil, err
				}
				current = op
				break
			}
		}

		// if we can't find the currently installed operator, we should treat it as a "new" subscription and skip treating it as already installed
		if current == nil {
			add[OperatorSourceInfo{
				Package:     sub.Spec.Package,
				Channel:     sub.Spec.Channel,
				StartingCSV: sub.Spec.StartingCSV,
				Catalog:     catalog,
			}] = struct{}{}
			continue
		}
		replacementInstallables, err := s.getReplacementInstallables(pkg, current, catalog, predicates, namespacedCache, visited)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, repInstallable := range replacementInstallables {
			installables = append(installables, repInstallable)
		}
	}

	// TODO: package installable building / replacement installable building can differ only in an additional "can replace" predicate

	// TODO: Consider csvs not attached to subscriptions

	for opToAdd := range add {
		pkg := opToAdd.Package
		//TODO: pass opToAdd.StartingCSV
		predicates := []OperatorPredicate{InChannel(pkg, opToAdd.Channel)}
		packageInstallables, err := s.getPackageInstallables(pkg, opToAdd.Catalog, predicates, namespacedCache, visited)
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
		if bundleInstallable, ok := installable.(*BundleInstallable); ok {
			operatorInstallables = append(operatorInstallables, *bundleInstallable)
		}
	}

	operators := make(map[string]OperatorSurface, 0)
	for _, installableOperator := range operatorInstallables {
		csvName, channel, catalog, err := installableOperator.BundleSourceInfo()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		op, err := ExactlyOne(namespacedCache.Catalog(catalog).Find(WithCSVName(csvName), WithChannel(channel)))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		op.replaces = installableOperator.Replaces
		operators[csvName] = op
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	return operators, nil
}

func (s *SatResolver) getReplacementInstallables(pkg string, current *Operator, catalog CatalogKey, predicates []OperatorPredicate, namespacedCache MultiCatalogOperatorFinder, visited map[OperatorSurface]*BundleInstallable) (map[string]solve.Installable, error) {
	installables := make(map[string]solve.Installable, 0)
	candidates := make([]*BundleInstallable, 0)

	// updates have the options: head of a channel (if skiprange matches), next replacement (if it exists), currently installed, in that order.
	virtReplacementInstallable := NewReplacementInstallable(pkg)

	bundles := namespacedCache.Catalog(catalog).Find(predicates...)

	if len(bundles) > 0 {
		sortedBundles, err := s.sortChannel(bundles)
		if err != nil {
			return nil, err
		}

		// add head if it includes the current in semver range
		head := sortedBundles[0]
		semverRange, err := semver.ParseRange(head.bundle.SkipRange)
		if err == nil && semverRange(*current.Version()) {
			headPredicates := append(predicates, WithCSVName(head.Identifier()))
			headId, headInstallable, err := s.getBundleInstallables(head.Identifier(), catalog, headPredicates, catalog, namespacedCache, visited)
			if err != nil {
				return nil, err
			}
			if len(headId) != 1 {
				// TODO better messages
				return nil, fmt.Errorf("trouble finding bundle for head")
			}

			for _, i := range headInstallable {
				if _, ok := headId[i.Identifier()]; ok {
					candidates = append(candidates, i)
				}
				installables[string(i.Identifier())] = i
			}
		}

		// add replacement if we have one
		var replacement *Operator
		for _, b := range bundles {
			if b.replaces == current.Identifier() {
				replacement = b
				break
			}
		}
		if replacement != nil {
			replacePredicates := append(predicates, WithCSVName(replacement.Identifier()))
			repId, repInstallable, err := s.getBundleInstallables(replacement.Identifier(), catalog, replacePredicates, catalog, namespacedCache, visited)
			if err != nil {
				return nil, err
			}
			if len(repId) != 1 {
				// TODO better messages
				return nil, fmt.Errorf("trouble finding bundle for replacement")
			}

			for _, i := range repInstallable {
				if _, ok := repId[i.Identifier()]; ok {
					candidates = append(candidates, i)
				}
				installables[string(i.Identifier())] = i
			}
		}
	}

	// TODO: is there a reason to track the current bundle as an installable (it is already installed)

	// TODO: this will be an issue if it is ever possible for one bundle to replace two different bundles that
	// are already installed in the cluster
	depIds := make([]solve.Identifier, 0)
	for _, c := range candidates {
		// track which operator this is replacing, so that it can be realized when creating the resources on cluster
		c.Replaces = current.Identifier()
		depIds = append(depIds, c.Identifier())
	}

	// all candiates added as options for this constraint
	virtReplacementInstallable.AddDependency(depIds)

	// installables now holds all options for the replacement, only one of which can be picked
	virtReplacementInstallable.ExactlyOne(candidates)

	installables[string(virtReplacementInstallable.Identifier())] = virtReplacementInstallable

	return installables, nil
}

func (s *SatResolver) getPackageInstallables(pkg string, catalog CatalogKey, predicates []OperatorPredicate, namespacedCache MultiCatalogOperatorFinder, visited map[OperatorSurface]*BundleInstallable) (map[string]solve.Installable, error) {
	var errs []error
	installables := make(map[string]solve.Installable, 0)
	virtualInstallable := NewVirtualPackageInstallable(pkg)

	// TODO: pass the filter into the cache to use startingcsv/latestcsv as limiters
	bundles := namespacedCache.Catalog(catalog).Find(predicates...)
	if len(bundles) == 0 {
		return nil, fmt.Errorf("no opts with pkg %s and channel TODO found in %s", pkg, catalog)
	}

	weightedBundles := s.sortBundles(bundles)

	virtDependencies := make(map[solve.Identifier]struct{}, 0)
	// add installable for each bundle version of the package
	// this is done to pin a mandatory solve to each required package
	for _, bundle := range weightedBundles {
		// traverse the dependency tree to generate the set of installables for given bundle version
		virtDependencyIdentifiers, bundleInstallables, err := s.getBundleInstallables(bundle.Identifier(), CatalogKey{}, predicates, catalog, namespacedCache, visited)
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

	// TODO: need an exactlyone constraint or a cardinality constraint
	virtualInstallable.AddDependencyFromSet(virtDependencies)
	installables[string(virtualInstallable.Identifier())] = virtualInstallable

	return installables, nil
}

func (s *SatResolver) getBundleInstallables(csvName string, catalog CatalogKey, predicates []OperatorPredicate, preferredCatalog CatalogKey, namespacedCache MultiCatalogOperatorFinder, visited map[OperatorSurface]*BundleInstallable) (map[solve.Identifier]struct{}, map[solve.Identifier]*BundleInstallable, error) {
	var errs []error
	installables := make(map[solve.Identifier]*BundleInstallable, 0) // aggregate all of the installables at every depth
	identifiers := make(map[solve.Identifier]struct{}, 0)            // keep track of depth + 1 dependencies

	var finder OperatorFinder = namespacedCache
	if !catalog.IsEmpty() {
		finder = namespacedCache.Catalog(catalog)
	}
	// TODO: verify that everywhere we call this already has this predicate
	bundles := finder.Find(append(predicates, WithCSVName(csvName))...)

	for _, bundle := range bundles {
		bundleSource := bundle.SourceInfo()
		if bundleSource == nil {
			err := fmt.Errorf("Unable to resolve the source of bundle %s, invalid cache", bundle.Identifier())
			errs = append(errs, err)
			continue
		}

		if b, ok := visited[bundle]; ok {
			installables[b.identifier] = b
			identifiers[b.Identifier()] = struct{}{}
			continue
		}

		bundleInstallable := NewBundleInstallable(csvName, bundle.bundle.ChannelName, bundleSource.Catalog)
		visited[bundle] = &bundleInstallable

		for _, depVersion := range bundle.VersionDependencies() {
			depCandidates, err := AtLeast(1, namespacedCache.Find(WithPackage(depVersion.Package), WithVersionInRange(depVersion.Version)))
			if err != nil {
				// If there are no candidates for a dependency, it means this bundle can't be resolved
				bundleInstallable.MakeProhibited()
				continue
			}

			bundleDependencies := make(map[solve.Identifier]struct{}, 0)
			for _, dep := range depCandidates {
				depIdentifiers, depInstallables, err := s.getBundleInstallables(dep.Identifier(), CatalogKey{}, []OperatorPredicate{}, preferredCatalog, namespacedCache, visited)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				for _, depInstallable := range depInstallables {
					// TODO: this check shouldn't be needed, depInstallable should point to the same instance
					if _, ok := installables[depInstallable.Identifier()]; !ok {
						installables[depInstallable.Identifier()] = depInstallable
					}
				}
				for depIdentifier := range depIdentifiers {
					bundleDependencies[depIdentifier] = struct{}{}
				}
			}
			// TODO: IMPORTANT: all dependencies (version + gvk) need to be added at once so that they are in one Dependency clause
			// currently this adds them seperately
			bundleInstallable.AddDependencyFromSet(bundleDependencies)
		}

		requiredAPIs := bundle.RequiredAPIs()
		for requiredAPI := range requiredAPIs {
			requiredAPICandidates, err := AtLeast(1, namespacedCache.Find(ProvidingAPI(requiredAPI)))
			if err != nil {
				// If there are no candidates for a dependency, it means this bundle can't be resolved
				bundleInstallable.MakeProhibited()
				continue
			}

			// sort requiredAPICandidates
			sortedCandidates := s.sortBundles(requiredAPICandidates)

			requiredAPIDependencies := make(map[solve.Identifier]struct{}, 0)
			for _, dep := range sortedCandidates {
				depIdentifiers, depInstallables, err := s.getBundleInstallables(dep.Identifier(), CatalogKey{}, []OperatorPredicate{}, preferredCatalog, namespacedCache, visited)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				for _, depInstallable := range depInstallables {
					// TODO: this check shouldn't be needed, depInstallable should point to the same instance
					if _, ok := installables[depInstallable.Identifier()]; !ok {
						installables[depInstallable.Identifier()] = depInstallable
					}
				}
				for depIdentifier := range depIdentifiers {
					requiredAPIDependencies[depIdentifier] = struct{}{}
				}
			}
			bundleInstallable.AddDependencyFromSet(requiredAPIDependencies)
		}
		installables[bundleInstallable.Identifier()] = &bundleInstallable
		identifiers[bundleInstallable.Identifier()] = struct{}{}
	}

	if len(errs) > 0 {
		return nil, nil, utilerrors.NewAggregate(errs)
	}

	return identifiers, installables, nil
}

func (s *SatResolver) sortBundles(bundles []*Operator) []*Operator {
	versionMap := make(map[string]*Operator, 0)
	versionSlice := make([]semver.Version, 0)
	unsortableList := make([]*Operator, 0)

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

	sortedBundles := make([]*Operator, 0)
	for _, sortedVersion := range versionSlice {
		sortedBundles = append(sortedBundles, versionMap[sortedVersion.String()])
	}
	for _, unsortable := range unsortableList {
		sortedBundles = append(sortedBundles, unsortable)
	}

	return sortedBundles
}

// sorts bundle in a channel by replaces
func (s *SatResolver) sortChannel(bundles []*Operator) ([]*Operator, error) {
	if len(bundles) <= 1 {
		return bundles, nil
	}

	channel := []*Operator{}

	bundleLookup := map[string]*Operator{}

	// if a replacedBy b, then replacedBy[b] = a
	replacedBy := map[*Operator]*Operator{}
	replaces := map[*Operator]*Operator{}

	for _, b := range bundles {
		bundleLookup[b.Identifier()] = b
	}

	for _, b := range bundles {
		if b.replaces == "" {
			continue
		}
		if r, ok := bundleLookup[b.replaces]; ok {
			replacedBy[r] = b
			replaces[b] = r
		}
	}

	// a bundle without a replacement is a channel head, but if we find more than one of those something is weird
	headCandidates := []*Operator{}
	for _, b := range bundles {
		if _, ok := replacedBy[b]; !ok {
			headCandidates = append(headCandidates, b)
		}
	}

	if len(headCandidates) != 1 {
		// TODO: more context in error
		return nil, fmt.Errorf("found more than one head for channel")
	}

	head := headCandidates[0]
	current := head
	for {
		channel = append(channel, current)
		next, ok := replaces[current]
		if !ok {
			break
		}
		current = next
	}

	// TODO: do we care if the channel doesn't include every bundle in the input?

	return channel, nil
}
