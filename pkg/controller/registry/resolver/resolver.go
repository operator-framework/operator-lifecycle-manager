package resolver

import (
	"context"
	"fmt"
	"github.com/blang/semver"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sort"
)

type OperatorResolver interface {
	SolveOperators(csvs []*v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription, add map[OperatorSourceInfo]struct{}) (OperatorSet, error)
}

type SatResolver struct {
	cache OperatorCacheProvider
	log logrus.FieldLogger
}

func NewDefaultSatResolver(rcp RegistryClientProvider, log logrus.FieldLogger) *SatResolver {
	return &SatResolver{
		cache: NewOperatorCache(rcp, log),
		log: log,
	}
}

type debugWriter struct {
	logrus.FieldLogger
}

func (w *debugWriter) Write(b []byte) (int, error) {
	n := len(b)
	w.Debug(b)
	return n, nil
}

func (r *SatResolver) SolveOperators(namespaces []string, csvs []*v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription) (OperatorSet, error) {
	var errs []error

	installables := make([]solver.Installable, 0)
	visited := make(map[OperatorSurface]*BundleInstallable, 0)

	// TODO: better abstraction
	startingCSVs := make(map[string]struct{})

	namespacedCache := r.cache.Namespaced(namespaces...)

	// build constraints for each Subscription
	for _, sub := range subs {
		pkg := sub.Spec.Package
		catalog := registry.CatalogKey{
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
		subInstallables, err := r.getSubscriptionInstallables(pkg, current, catalog, predicates, channelFilter, namespacedCache, visited)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, i := range subInstallables {
			installables = append(installables, i)
		}
	}

	// TODO: Consider csvs not attached to subscriptions

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	s, err := solver.New(solver.WithInput(installables), solver.WithTracer(solver.LoggingTracer{&debugWriter{r.log}}))
	if err != nil {
		return nil, err
	}
	solvedInstallables, err := s.Solve(context.TODO())
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

func (r *SatResolver) getSubscriptionInstallables(pkg string, current *Operator, catalog registry.CatalogKey, cachePredicates []OperatorPredicate, channelPredicates []OperatorPredicate, namespacedCache MultiCatalogOperatorFinder, visited map[OperatorSurface]*BundleInstallable) (map[string]solver.Installable, error) {
	installables := make(map[string]solver.Installable, 0)
	candidates := make([]*BundleInstallable, 0)

	subInstallable := NewSubscriptionInstallable(pkg)
	installables[string(subInstallable.Identifier())] = &subInstallable

	bundles := namespacedCache.Catalog(catalog).Find(cachePredicates...)

	// there are no options for this package, return early
	if len(bundles) == 0 {
		return installables, nil
	}

	sortedBundles, err := r.sortChannel(bundles)
	if err != nil {
		return nil, err
	}

	for _, o := range Filter(sortedBundles, channelPredicates...) {
		predicates := append(cachePredicates, WithCSVName(o.Identifier()))
		id, installable, err := r.getBundleInstallables(catalog, predicates, namespacedCache, visited)
		if err != nil {
			return nil, err
		}
		if len(id) < 1 {
			return nil, fmt.Errorf("could not find any potential bundles for subscription: %s", pkg)
		}

		for _, i := range installable {
			if _, ok := id[i.Identifier()]; ok {
				candidates = append(candidates, i)
			}
			installables[string(i.Identifier())] = i
		}
	}

	depIds := make([]solver.Identifier, 0)
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

func (r *SatResolver) getBundleInstallables(catalog registry.CatalogKey, predicates []OperatorPredicate, namespacedCache MultiCatalogOperatorFinder, visited map[OperatorSurface]*BundleInstallable) (map[solver.Identifier]struct{}, map[solver.Identifier]*BundleInstallable, error) {
	errs := []error{}
	installables := make(map[solver.Identifier]*BundleInstallable, 0) // all installables, including dependencies

	var finder OperatorFinder = namespacedCache
	if !catalog.IsEmpty() {
		finder = namespacedCache.Catalog(catalog)
	}

	bundleStack := finder.Find(predicates...)

	// track the first layer of installable ids
	var initial = make(map[*Operator]struct{})
	for _, o := range bundleStack {
		initial[o] = struct{}{}
	}

	for {
		if len(bundleStack) == 0 {
			break
		}
		// pop from the stack
		bundle := bundleStack[len(bundleStack)-1]
		bundleStack = bundleStack[:len(bundleStack)-1]

		bundleSource := bundle.SourceInfo()
		if bundleSource == nil {
			err := fmt.Errorf("unable to resolve the source of bundle %s, invalid cache", bundle.Identifier())
			errs = append(errs, err)
			continue
		}

		if b, ok := visited[bundle]; ok {
			installables[b.identifier] = b
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
			// errors ignored; this will build an empty/unsatisfiable dependency if no candidates are found
			candidateBundles, _ := AtLeast(1, namespacedCache.FindPreferred(&bundleSource.Catalog, d))
			// TODO: candidates can be in multiple catalogs. This sort account for catalog order / channel order / etc
			r.sortByVersion(candidateBundles)
			bundleDependencies := make(map[solver.Identifier]struct{}, 0)
			for _, dep := range candidateBundles {
				found := namespacedCache.Catalog(dep.SourceInfo().Catalog).Find(WithCSVName(dep.Identifier()))
				if len(found) > 1 {
					err := fmt.Errorf("found duplicate entries for %s in %s", bundle.Identifier(), dep.sourceInfo.Catalog)
					errs = append(errs, err)
					continue
				}
				b := found[0]
				src := b.SourceInfo()
				if src == nil {
					err := fmt.Errorf("unable to resolve the source of bundle %s, invalid cache", bundle.Identifier())
					errs = append(errs, err)
					continue
				}

				i := NewBundleInstallable(b.Identifier(), b.bundle.ChannelName, src.Catalog)
				installables[i.Identifier()] = &i
				bundleDependencies[i.Identifier()] = struct{}{}
				bundleStack = append(bundleStack, b)
			}

			bundleInstallable.AddDependencyFromSet(bundleDependencies)
		}

		installables[bundleInstallable.Identifier()] = &bundleInstallable
	}

	if len(errs) > 0 {
		return nil, nil, utilerrors.NewAggregate(errs)
	}

	ids := make(map[solver.Identifier]struct{}, 0) // immediate installables found via predicates
	for o := range initial {
		ids[visited[o].Identifier()] = struct{}{}
	}

	return ids, installables, nil
}

func (r *SatResolver) sortByVersion(bundles []*Operator) {
	zeroVersion, _ := semver.Make("")

	// sort descending
	sort.Slice(bundles, func(i, j int) bool {
		iv := bundles[i].Version()
		jv := bundles[j].Version()
		if iv.Equals(zeroVersion) || jv.Equals(zeroVersion) {
			return bundles[i].Identifier() > bundles[j].Identifier()
		}
		return iv.GT(*jv)
	})
}

// sorts bundle in a channel by replaces
func (r *SatResolver) sortChannel(bundles []*Operator) ([]*Operator, error) {
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
