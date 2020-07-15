package resolver

import (
	"context"
	"fmt"
	"github.com/sirupsen/logrus"

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
	log logrus.FieldLogger
}

func NewDefaultSatResolver(rcp RegistryClientProvider, log logrus.FieldLogger) *SatResolver {
	return &SatResolver{
		cache: NewOperatorCache(rcp),
		log: log,
	}
}

type debugWriter struct {
	logrus.FieldLogger
}

func (w *debugWriter) Write(b []byte) (int, error) {
	n := len(b)
	if n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	w.Debug(b)
	return n, nil
}

func (s *SatResolver) SolveOperators(namespaces []string, csvs []*v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription) (OperatorSet, error) {
	var errs []error

	installables := make([]solve.Installable, 0)
	visited := make(map[OperatorSurface]*BundleInstallable, 0)

	// TODO: better abstraction
	startingCSVs := make(map[string]struct{})

	namespacedCache := s.cache.Namespaced(namespaces...)

	// build constraints for each Subscription
	for _, sub := range subs {
		pkg := sub.Spec.Package
		catalog := CatalogKey{
			Name:      sub.Spec.CatalogSource,
			Namespace: sub.Spec.CatalogSourceNamespace,
		}
		predicates := []OperatorPredicate{InChannel(pkg, sub.Spec.Channel)}

		// find the currently installed operator (if it exists)
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

		channelFilter := []OperatorPredicate{}

		// if we found an existing installed operator, we should filter the channel by operators that can replace it
		if current != nil {
			channelFilter = append(channelFilter, Or(SkipRangeIncludes(*current.Version()), Replaces(current.Identifier())))
		}

		// if no operator is installed and we have a startingCSV, filter for it
		if current == nil && len(sub.Spec.StartingCSV) > 0 {
			channelFilter = append(channelFilter, WithCSVName(sub.Spec.StartingCSV))
			startingCSVs[sub.Spec.StartingCSV] = struct{}{}
		}

		// find operators, in channel order, that can skip from the current version or list the current in "replaces"
		replacementInstallables, err := s.getSubscriptionInstallables(pkg, current, catalog, predicates, channelFilter, namespacedCache, visited)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, repInstallable := range replacementInstallables {
			installables = append(installables, repInstallable)
		}
	}

	// TODO: Consider csvs not attached to subscriptions

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	solver, err := solve.New(solve.WithInput(installables), solve.WithTracer(solve.LoggingTracer{&debugWriter{s.log}}))
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
		if len(installableOperator.Replaces) > 0 {
			op.replaces = installableOperator.Replaces
		}

		// lookup if this installable came from a starting CSV
		if _, ok := startingCSVs[csvName]; ok {
			op.sourceInfo.StartingCSV = csvName
		}

		operators[csvName] = op
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	return operators, nil
}

func (s *SatResolver) getSubscriptionInstallables(pkg string, current *Operator, catalog CatalogKey, cachePredicates []OperatorPredicate, channelPredicates []OperatorPredicate, namespacedCache MultiCatalogOperatorFinder, visited map[OperatorSurface]*BundleInstallable) (map[string]solve.Installable, error) {
	installables := make(map[string]solve.Installable, 0)
	candidates := make([]*BundleInstallable, 0)

	subInstallable := NewSubscriptionInstallable(pkg)
	installables[string(subInstallable.Identifier())] = &subInstallable

	bundles := namespacedCache.Catalog(catalog).Find(cachePredicates...)

	// there are no options for this package, return early
	if len(bundles) == 0 {
		return installables, nil
	}

	sortedBundles, err := s.sortChannel(bundles)
	if err != nil {
		return nil, err
	}

	for _, o := range Filter(sortedBundles, channelPredicates...) {
		predicates := append(cachePredicates, WithCSVName(o.Identifier()))
		id, installable, err := s.getBundleInstallables(catalog, predicates, catalog, namespacedCache, visited)
		if err != nil {
			return nil, err
		}
		if len(id) != 1 {
			// TODO better messages
			return nil, fmt.Errorf("trouble generating installable for potential replacement bundle")
		}

		for _, i := range installable {
			if _, ok := id[i.Identifier()]; ok {
				candidates = append(candidates, i)
			}
			installables[string(i.Identifier())] = i
		}
	}

	depIds := make([]solve.Identifier, 0)
	for _, c := range candidates {
		// track which operator this is replacing, so that it can be realized when creating the resources on cluster
		if current != nil {
			c.Replaces = current.Identifier()
		}
		depIds = append(depIds, c.Identifier())
	}

	// all candidates added as options for this constraint
	subInstallable.AddDependency(depIds)

	return installables, nil
}

func (s *SatResolver) getBundleInstallables(catalog CatalogKey, predicates []OperatorPredicate, preferredCatalog CatalogKey, namespacedCache MultiCatalogOperatorFinder, visited map[OperatorSurface]*BundleInstallable) (map[solve.Identifier]struct{}, map[solve.Identifier]*BundleInstallable, error) {
	var errs []error
	installables := make(map[solve.Identifier]*BundleInstallable, 0) // aggregate all of the installables at every depth
	identifiers := make(map[solve.Identifier]struct{}, 0)            // keep track of depth + 1 dependencies

	var finder OperatorFinder = namespacedCache
	if !catalog.IsEmpty() {
		finder = namespacedCache.Catalog(catalog)
	}

	bundleStack := finder.Find(predicates...)
	for _, bundle := range bundleStack {
		bundleStack = bundleStack[:len(bundleStack)-1]

		bundleSource := bundle.SourceInfo()
		if bundleSource == nil {
			err := fmt.Errorf("unable to resolve the source of bundle %s, invalid cache", bundle.Identifier())
			errs = append(errs, err)
			continue
		}

		if b, ok := visited[bundle]; ok {
			installables[b.identifier] = b
			identifiers[b.Identifier()] = struct{}{}
			continue
		}

		bundleInstallable := NewBundleInstallable(bundle.Identifier(), bundle.bundle.ChannelName, bundleSource.Catalog)
		visited[bundle] = &bundleInstallable

		dependencyPredicates, err := bundle.DependencyPredicates()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, d := range dependencyPredicates {
			candidateBundles, err := AtLeast(1, namespacedCache.Find(d))
			if err != nil {
				// If there are no candidates for a dependency, it means this bundle can't be resolved
				bundleInstallable.MakeProhibited()
				continue
			}

			bundleDependencies := make(map[solve.Identifier]struct{}, 0)
			for _, dep := range candidateBundles {
				// TODO: search in preferred catalog
				candidateBundles := finder.Find(WithCSVName(dep.Identifier()))

				sortedCandidates := s.sortByVersion(candidateBundles)

				for _, b := range sortedCandidates {
					src := b.SourceInfo()
					if src == nil {
						err := fmt.Errorf("unable to resolve the source of bundle %s, invalid cache", bundle.Identifier())
						errs = append(errs, err)
						continue
					}

					i := NewBundleInstallable(b.Identifier(), b.bundle.ChannelName, bundleSource.Catalog)
					installables[i.Identifier()] = &i
					bundleDependencies[i.Identifier()] = struct{}{}
					bundleStack = append(bundleStack, b)
				}
			}

			// TODO: IMPORTANT: current a solver bug will skip later dependency clauses
			bundleInstallable.AddDependencyFromSet(bundleDependencies)
		}

		installables[bundleInstallable.Identifier()] = &bundleInstallable
		identifiers[bundleInstallable.Identifier()] = struct{}{}
	}

	if len(errs) > 0 {
		return nil, nil, utilerrors.NewAggregate(errs)
	}

	return identifiers, installables, nil
}

func (s *SatResolver) sortByVersion(bundles []*Operator) []*Operator {
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
