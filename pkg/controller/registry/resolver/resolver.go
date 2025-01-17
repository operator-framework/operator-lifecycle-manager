package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/api/pkg/constraints"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

// constraintProvider knows how to provide solver constraints for a given cache entry.
// For instance, it could be used to surface additional constraints against an entry given some
// properties it may expose. E.g. olm.maxOpenShiftVersion could be checked against the cluster version
// and prohibit any entry that doesn't meet the requirement
type constraintProvider interface {
	// Constraints returns a set of solver constraints for a cache entry.
	Constraints(e *cache.Entry) ([]solver.Constraint, error)
}

type Resolver struct {
	cache                     cache.OperatorCacheProvider
	log                       logrus.FieldLogger
	pc                        *predicateConverter
	systemConstraintsProvider constraintProvider
}

func NewDefaultResolver(cacheProvider cache.OperatorCacheProvider, logger logrus.FieldLogger) *Resolver {
	return &Resolver{
		cache: cacheProvider,
		log:   logger,
		pc: &predicateConverter{
			celEnv: constraints.NewCelEnvironment(),
		},
	}
}

type debugWriter struct {
	logrus.FieldLogger
}

func (w *debugWriter) Write(b []byte) (int, error) {
	n := len(b)
	w.Debug(string(b))
	return n, nil
}

func (r *Resolver) Resolve(namespaces []string, subs []*v1alpha1.Subscription) ([]*cache.Entry, error) {
	var errs []error

	variables := make(map[solver.Identifier]solver.Variable)
	visited := make(map[*cache.Entry]*BundleVariable)

	// TODO: better abstraction
	startingCSVs := make(map[string]struct{})

	namespacedCache := r.cache.Namespaced(namespaces...)

	if len(namespaces) < 1 {
		// the first namespace is treated as the preferred namespace today
		return nil, fmt.Errorf("at least one namespace must be provided to resolution")
	}

	preferredNamespace := namespaces[0]
	_, existingVariables, err := r.getBundleVariables(preferredNamespace, namespacedCache.Catalog(cache.NewVirtualSourceKey(preferredNamespace)).Find(cache.True()), namespacedCache, visited)
	if err != nil {
		return nil, err
	}
	for _, i := range existingVariables {
		variables[i.Identifier()] = i
	}

	// build constraints for each Subscription
	for _, sub := range subs {
		// find the currently installed operator (if it exists)
		var current *cache.Entry

		matches := namespacedCache.Catalog(cache.NewVirtualSourceKey(sub.Namespace)).Find(cache.CSVNamePredicate(sub.Status.InstalledCSV))
		if len(matches) > 1 {
			var names []string
			for _, each := range matches {
				names = append(names, each.Name)
			}
			return nil, fmt.Errorf("multiple name matches for status.installedCSV of subscription %s/%s: %s", sub.Namespace, sub.Name, strings.Join(names, ", "))
		} else if len(matches) == 1 {
			current = matches[0]
		}

		if current == nil && sub.Spec.StartingCSV != "" {
			startingCSVs[sub.Spec.StartingCSV] = struct{}{}
		}

		// find operators, in channel order, that can skip from the current version or list the current in "replaces"
		subVariables, err := r.getSubscriptionVariables(sub, current, namespacedCache, visited)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, i := range subVariables {
			variables[i.Identifier()] = i
		}
	}

	r.addInvariants(namespacedCache, variables)

	if err := namespacedCache.Error(); err != nil {
		return nil, err
	}

	input := make([]solver.Variable, 0)
	for _, i := range variables {
		input = append(input, i)
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	s, err := solver.New(solver.WithInput(input), solver.WithTracer(solver.LoggingTracer{Writer: &debugWriter{r.log}}))
	if err != nil {
		return nil, err
	}
	solvedVariables, err := s.Solve(context.TODO())
	if err != nil {
		return nil, err
	}

	// get the set of bundle variables from the result solved variables
	operatorVariables := make([]BundleVariable, 0)
	for _, variable := range solvedVariables {
		if bundleVariable, ok := variable.(*BundleVariable); ok {
			_, _, catalog, err := bundleVariable.BundleSourceInfo()
			if err != nil {
				return nil, fmt.Errorf("error determining origin of operator: %w", err)
			}
			if catalog.Virtual() {
				// Result is expected to contain only new things.
				continue
			}
			operatorVariables = append(operatorVariables, *bundleVariable)
		}
	}

	var operators []*cache.Entry
	for _, variableOperator := range operatorVariables {
		csvName, channel, catalog, err := variableOperator.BundleSourceInfo()
		if err != nil {
			errs = append(errs, err)
			continue
		}

		op, err := cache.ExactlyOne(namespacedCache.Catalog(catalog).Find(cache.CSVNamePredicate(csvName), cache.ChannelPredicate(channel)))
		if err != nil {
			errs = append(errs, err)
			continue
		}

		// copy consumed fields to avoid directly mutating cache
		op = &cache.Entry{
			Name:         op.Name,
			Replaces:     op.Replaces,
			Skips:        op.Skips,
			SkipRange:    op.SkipRange,
			ProvidedAPIs: op.ProvidedAPIs,
			RequiredAPIs: op.RequiredAPIs,
			Version:      op.Version,
			SourceInfo: &cache.OperatorSourceInfo{
				Package:        op.SourceInfo.Package,
				Channel:        op.SourceInfo.Channel,
				StartingCSV:    op.SourceInfo.StartingCSV,
				Catalog:        op.SourceInfo.Catalog,
				DefaultChannel: op.SourceInfo.DefaultChannel,
				Subscription:   op.SourceInfo.Subscription,
			},
			Properties: op.Properties,
			BundlePath: op.BundlePath,
			Bundle:     op.Bundle,
		}
		if len(variableOperator.Replaces) > 0 {
			op.Replaces = variableOperator.Replaces
		}

		// lookup if this variable came from a starting CSV
		if _, ok := startingCSVs[csvName]; ok {
			op.SourceInfo.StartingCSV = csvName
		}

		operators = append(operators, op)
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	return operators, nil
}

// newBundleVariableFromEntry converts an entry into a bundle variable with
// system constraints applied, if they are defined for the entry
func (r *Resolver) newBundleVariableFromEntry(entry *cache.Entry) (*BundleVariable, error) {
	bundleInstalleble, err := NewBundleVariableFromOperator(entry)
	if err != nil {
		return nil, err
	}

	// apply system constraints if necessary
	if r.systemConstraintsProvider != nil && !(entry.SourceInfo.Catalog.Virtual()) {
		systemConstraints, err := r.systemConstraintsProvider.Constraints(entry)
		if err != nil {
			return nil, err
		}
		bundleInstalleble.constraints = append(bundleInstalleble.constraints, systemConstraints...)
	}
	return &bundleInstalleble, nil
}

func (r *Resolver) getSubscriptionVariables(sub *v1alpha1.Subscription, current *cache.Entry, namespacedCache cache.MultiCatalogOperatorFinder, visited map[*cache.Entry]*BundleVariable) (map[solver.Identifier]solver.Variable, error) {
	var cachePredicates, channelPredicates []cache.Predicate
	variables := make(map[solver.Identifier]solver.Variable)

	catalog := cache.SourceKey{
		Name:      sub.Spec.CatalogSource,
		Namespace: sub.Spec.CatalogSourceNamespace,
	}

	var entries []*cache.Entry
	{
		var nall, npkg, nch, ncsv int

		csvPredicate := cache.True()
		if current != nil {
			// if we found an existing installed operator, we should filter the channel by operators that can replace it
			channelPredicates = append(channelPredicates, cache.Or(cache.SkipRangeIncludesPredicate(*current.Version), cache.ReplacesPredicate(current.Name)))
		} else if sub.Spec.StartingCSV != "" {
			// if no operator is installed and we have a startingCSV, filter for it
			csvPredicate = cache.CSVNamePredicate(sub.Spec.StartingCSV)
		}

		cachePredicates = append(cachePredicates, cache.And(
			cache.CountingPredicate(cache.True(), &nall),
			cache.CountingPredicate(cache.PkgPredicate(sub.Spec.Package), &npkg),
			cache.CountingPredicate(cache.ChannelPredicate(sub.Spec.Channel), &nch),
			cache.CountingPredicate(csvPredicate, &ncsv),
		))
		entries = namespacedCache.Catalog(catalog).Find(cachePredicates...)

		var si solver.Variable
		switch {
		case nall == 0:
			si = NewInvalidSubscriptionVariable(sub.GetName(), fmt.Sprintf("no operators found from catalog %s in namespace %s referenced by subscription %s", sub.Spec.CatalogSource, sub.Spec.CatalogSourceNamespace, sub.GetName()))
		case npkg == 0:
			si = NewInvalidSubscriptionVariable(sub.GetName(), fmt.Sprintf("no operators found in package %s in the catalog referenced by subscription %s", sub.Spec.Package, sub.GetName()))
		case nch == 0:
			si = NewInvalidSubscriptionVariable(sub.GetName(), fmt.Sprintf("no operators found in channel %s of package %s in the catalog referenced by subscription %s", sub.Spec.Channel, sub.Spec.Package, sub.GetName()))
		case ncsv == 0:
			si = NewInvalidSubscriptionVariable(sub.GetName(), fmt.Sprintf("no operators found with name %s in channel %s of package %s in the catalog referenced by subscription %s", sub.Spec.StartingCSV, sub.Spec.Channel, sub.Spec.Package, sub.GetName()))
		}

		if si != nil {
			variables[si.Identifier()] = si
			return variables, nil
		}
	}

	// entries in the default channel appear first, then lexicographically order by channel name
	sort.SliceStable(entries, func(i, j int) bool {
		var idef bool
		var ichan string
		if isrc := entries[i].SourceInfo; isrc != nil {
			idef = isrc.DefaultChannel
			ichan = isrc.Channel
		}
		var jdef bool
		var jchan string
		if jsrc := entries[j].SourceInfo; jsrc != nil {
			jdef = jsrc.DefaultChannel
			jchan = jsrc.Channel
		}
		if idef == jdef {
			return ichan < jchan
		}
		return idef
	})

	var sortedBundles []*cache.Entry
	lastChannel, lastIndex := "", 0
	for i := 0; i <= len(entries); i++ {
		if i != len(entries) && entries[i].Channel() == lastChannel {
			continue
		}
		channel, err := sortChannel(entries[lastIndex:i])
		if err != nil {
			return nil, err
		}
		sortedBundles = append(sortedBundles, channel...)

		if i != len(entries) {
			lastChannel = entries[i].Channel()
			lastIndex = i
		}
	}

	candidates := make([]*BundleVariable, 0)
	for _, o := range cache.Filter(sortedBundles, channelPredicates...) {
		predicates := append(cachePredicates, cache.CSVNamePredicate(o.Name))
		stack := namespacedCache.Catalog(catalog).Find(predicates...)
		id, variable, err := r.getBundleVariables(sub.Namespace, stack, namespacedCache, visited)
		if err != nil {
			return nil, err
		}
		if len(id) < 1 {
			return nil, fmt.Errorf("could not find any potential bundles for subscription: %s", sub.Spec.Package)
		}

		for _, i := range variable {
			if _, ok := id[i.Identifier()]; ok {
				candidates = append(candidates, i)
			}
			variables[i.Identifier()] = i
		}
	}

	depIds := make([]solver.Identifier, 0)
	for _, c := range candidates {
		// track which operator this is replacing, so that it can be realized when creating the resources on cluster
		if current != nil {
			c.Replaces = current.Name
			// Package name can't be reliably inferred
			// from a CSV without a projected package
			// property, so for the replacement case, a
			// one-to-one conflict is created between the
			// replacer and the replacee. It should be
			// safe to remove this conflict if properties
			// annotations are made mandatory for
			// resolution.
			c.AddConflict(bundleID(current.Name, current.Channel(), cache.NewVirtualSourceKey(sub.GetNamespace())))
		}
		depIds = append(depIds, c.Identifier())
	}
	if current != nil {
		depIds = append(depIds, bundleID(current.Name, current.Channel(), cache.NewVirtualSourceKey(sub.GetNamespace())))
	}

	// all candidates added as options for this constraint
	subVariable := NewSubscriptionVariable(sub.GetName(), depIds)
	variables[subVariable.Identifier()] = subVariable

	return variables, nil
}

func (r *Resolver) getBundleVariables(preferredNamespace string, bundleStack []*cache.Entry, namespacedCache cache.MultiCatalogOperatorFinder, visited map[*cache.Entry]*BundleVariable) (map[solver.Identifier]struct{}, map[solver.Identifier]*BundleVariable, error) {
	errs := make([]error, 0)
	variables := make(map[solver.Identifier]*BundleVariable) // all variables, including dependencies

	// track the first layer of variable ids
	var initial = make(map[*cache.Entry]struct{})
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

		if b, ok := visited[bundle]; ok {
			variables[b.identifier] = b
			continue
		}

		bundleVariable, err := r.newBundleVariableFromEntry(bundle)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		visited[bundle] = bundleVariable

		dependencyPredicates, err := r.pc.convertDependencyProperties(bundle.Properties)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, d := range dependencyPredicates {
			sourcePredicate := cache.False()
			// Build a filter matching all (catalog,
			// package, channel) combinations that contain
			// at least one candidate bundle, even if only
			// a subset of those bundles actually satisfy
			// the dependency.
			sources := map[cache.OperatorSourceInfo]struct{}{}
			for _, b := range namespacedCache.Find(d) {
				si := b.SourceInfo

				if _, ok := sources[*si]; ok {
					// Predicate already covers this source.
					continue
				}
				sources[*si] = struct{}{}

				if si.Catalog.Virtual() {
					sourcePredicate = cache.Or(sourcePredicate, cache.And(
						cache.CSVNamePredicate(b.Name),
						cache.CatalogPredicate(si.Catalog),
					))
				} else {
					sourcePredicate = cache.Or(sourcePredicate, cache.And(
						cache.PkgPredicate(si.Package),
						cache.ChannelPredicate(si.Channel),
						cache.CatalogPredicate(si.Catalog),
					))
				}
			}

			sortedBundles, err := r.sortBundles(namespacedCache.FindPreferred(&bundle.SourceInfo.Catalog, preferredNamespace, sourcePredicate))
			if err != nil {
				errs = append(errs, err)
				continue
			}
			bundleDependencies := make([]solver.Identifier, 0)
			// The dependency predicate is applied here
			// (after sorting) to remove all bundles that
			// don't satisfy the dependency.
			for _, b := range cache.Filter(sortedBundles, d) {
				i, err := r.newBundleVariableFromEntry(b)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				variables[i.Identifier()] = i
				bundleDependencies = append(bundleDependencies, i.Identifier())
				bundleStack = append(bundleStack, b)
			}
			bundleVariable.AddConstraint(PrettyConstraint(
				solver.Dependency(bundleDependencies...),
				fmt.Sprintf("bundle %s requires an operator %s", bundle.Name, d.String()),
			))
		}

		variables[bundleVariable.Identifier()] = bundleVariable
	}

	if len(errs) > 0 {
		return nil, nil, utilerrors.NewAggregate(errs)
	}

	ids := make(map[solver.Identifier]struct{}) // immediate variables found via predicates
	for o := range initial {
		ids[visited[o].Identifier()] = struct{}{}
	}

	return ids, variables, nil
}

func (r *Resolver) addInvariants(namespacedCache cache.MultiCatalogOperatorFinder, variables map[solver.Identifier]solver.Variable) {
	// no two operators may provide the same GVK or Package in a namespace
	gvkConflictToVariable := make(map[opregistry.GVKProperty][]solver.Identifier)
	packageConflictToVariable := make(map[string][]solver.Identifier)
	for _, variable := range variables {
		bundleVariable, ok := variable.(*BundleVariable)
		if !ok {
			continue
		}
		csvName, channel, catalog, err := bundleVariable.BundleSourceInfo()
		if err != nil {
			continue
		}

		op, err := cache.ExactlyOne(namespacedCache.Catalog(catalog).Find(cache.CSVNamePredicate(csvName), cache.ChannelPredicate(channel)))
		if err != nil {
			continue
		}

		// cannot provide the same GVK
		for _, p := range op.Properties {
			if p.Type != opregistry.GVKType {
				continue
			}
			var prop opregistry.GVKProperty
			err := json.Unmarshal([]byte(p.Value), &prop)
			if err != nil {
				continue
			}
			gvkConflictToVariable[prop] = append(gvkConflictToVariable[prop], variable.Identifier())
		}

		// cannot have the same package
		for _, p := range op.Properties {
			if p.Type != opregistry.PackageType {
				continue
			}
			var prop opregistry.PackageProperty
			err := json.Unmarshal([]byte(p.Value), &prop)
			if err != nil {
				continue
			}
			packageConflictToVariable[prop.PackageName] = append(packageConflictToVariable[prop.PackageName], variable.Identifier())
		}
	}

	for gvk, is := range gvkConflictToVariable {
		slices.Sort(is)
		s := NewSingleAPIProviderVariable(gvk.Group, gvk.Version, gvk.Kind, is)
		variables[s.Identifier()] = s
	}

	for pkg, is := range packageConflictToVariable {
		slices.Sort(is)
		s := NewSinglePackageInstanceVariable(pkg, is)
		variables[s.Identifier()] = s
	}
}

func (r *Resolver) sortBundles(bundles []*cache.Entry) ([]*cache.Entry, error) {
	// assume bundles have been passed in sorted by catalog already
	catalogOrder := make([]cache.SourceKey, 0)

	type PackageChannel struct {
		Package, Channel string
		DefaultChannel   bool
	}
	// TODO: for now channels will be sorted lexicographically
	channelOrder := make(map[cache.SourceKey][]PackageChannel)

	// partition by catalog -> channel -> bundle
	partitionedBundles := map[cache.SourceKey]map[PackageChannel][]*cache.Entry{}
	for _, b := range bundles {
		pc := PackageChannel{
			Package:        b.Package(),
			Channel:        b.Channel(),
			DefaultChannel: b.SourceInfo.DefaultChannel,
		}
		if _, ok := partitionedBundles[b.SourceInfo.Catalog]; !ok {
			catalogOrder = append(catalogOrder, b.SourceInfo.Catalog)
			partitionedBundles[b.SourceInfo.Catalog] = make(map[PackageChannel][]*cache.Entry)
		}
		if _, ok := partitionedBundles[b.SourceInfo.Catalog][pc]; !ok {
			channelOrder[b.SourceInfo.Catalog] = append(channelOrder[b.SourceInfo.Catalog], pc)
			partitionedBundles[b.SourceInfo.Catalog][pc] = make([]*cache.Entry, 0)
		}
		partitionedBundles[b.SourceInfo.Catalog][pc] = append(partitionedBundles[b.SourceInfo.Catalog][pc], b)
	}

	for catalog := range partitionedBundles {
		sort.SliceStable(channelOrder[catalog], func(i, j int) bool {
			pi, pj := channelOrder[catalog][i], channelOrder[catalog][j]
			if pi.DefaultChannel != pj.DefaultChannel {
				return pi.DefaultChannel
			}
			if pi.Package != pj.Package {
				return pi.Package < pj.Package
			}
			return pi.Channel < pj.Channel
		})
		for channel := range partitionedBundles[catalog] {
			sorted, err := sortChannel(partitionedBundles[catalog][channel])
			if err != nil {
				return nil, err
			}
			partitionedBundles[catalog][channel] = sorted
		}
	}
	all := make([]*cache.Entry, 0)
	for _, catalog := range catalogOrder {
		for _, channel := range channelOrder[catalog] {
			all = append(all, partitionedBundles[catalog][channel]...)
		}
	}
	return all, nil
}

// Sorts bundle in a channel by replaces. All entries in the argument
// are assumed to have the same Package and Channel.
func sortChannel(bundles []*cache.Entry) ([]*cache.Entry, error) {
	if len(bundles) < 1 {
		return bundles, nil
	}

	packageName := bundles[0].Package()
	channelName := bundles[0].Channel()

	bundleLookup := map[string]*cache.Entry{}

	// if a replaces b, then replacedBy[b] = a
	replacedBy := map[*cache.Entry]*cache.Entry{}
	replaces := map[*cache.Entry]*cache.Entry{}
	skipped := map[string]*cache.Entry{}

	for _, b := range bundles {
		bundleLookup[b.Name] = b
	}

	for _, b := range bundles {
		if b.Replaces != "" {
			if r, ok := bundleLookup[b.Replaces]; ok {
				replacedBy[r] = b
				replaces[b] = r
			}
		}
		for _, skip := range b.Skips {
			if r, ok := bundleLookup[skip]; ok {
				replacedBy[r] = b
				skipped[skip] = r
			}
		}
	}

	// a bundle without a replacement is a channel head, but if we
	// find more than one of those something is weird
	headCandidates := []*cache.Entry{}
	for _, b := range bundles {
		if _, ok := replacedBy[b]; !ok {
			headCandidates = append(headCandidates, b)
		}
	}
	if len(headCandidates) == 0 {
		return nil, fmt.Errorf("no channel heads (entries not replaced by another entry) found in channel %q of package %q", channelName, packageName)
	}

	var chains [][]*cache.Entry
	for _, head := range headCandidates {
		var chain []*cache.Entry
		visited := make(map[*cache.Entry]struct{})
		current := head
		for {
			visited[current] = struct{}{}
			if _, ok := skipped[current.Name]; !ok {
				chain = append(chain, current)
			}
			next, ok := replaces[current]
			if !ok {
				break
			}
			if _, ok := visited[next]; ok {
				return nil, fmt.Errorf("a cycle exists in the chain of replacement beginning with %q in channel %q of package %q", head.Name, channelName, packageName)
			}
			current = next
		}
		chains = append(chains, chain)
	}

	if len(chains) > 1 {
		schains := make([]string, len(chains))
		for i, chain := range chains {
			switch len(chain) {
			case 0:
				schains[i] = "[]" // Bug?
			case 1:
				schains[i] = chain[0].Name
			default:
				schains[i] = fmt.Sprintf("%s...%s", chain[0].Name, chain[len(chain)-1].Name)
			}
		}
		return nil, fmt.Errorf("a unique replacement chain within a channel is required to determine the relative order between channel entries, but %d replacement chains were found in channel %q of package %q: %s", len(schains), channelName, packageName, strings.Join(schains, ", "))
	}

	if len(chains) == 0 {
		// Bug?
		return nil, fmt.Errorf("found no replacement chains in channel %q of package %q", channelName, packageName)
	}

	// TODO: do we care if the channel doesn't include every bundle in the input?
	return chains[0], nil
}

// predicateConverter configures olm.constraint value -> predicate conversion for the resolver.
type predicateConverter struct {
	celEnv *constraints.CelEnvironment
}

// convertDependencyProperties converts all known constraint properties to predicates.
func (pc *predicateConverter) convertDependencyProperties(properties []*api.Property) ([]cache.Predicate, error) {
	var predicates []cache.Predicate
	for _, property := range properties {
		predicate, err := pc.predicateForProperty(property)
		if err != nil {
			return nil, err
		}
		if predicate == nil {
			continue
		}
		predicates = append(predicates, predicate)
	}
	return predicates, nil
}

func (pc *predicateConverter) predicateForProperty(property *api.Property) (cache.Predicate, error) {
	if property == nil {
		return nil, nil
	}

	// olm.constraint holds all constraint types except legacy types,
	// so defer error handling to its parser.
	if property.Type == constraints.OLMConstraintType {
		return pc.predicateForConstraintProperty(property.Value)
	}

	// Legacy behavior dictates that unknown properties are ignored. See enhancement for details:
	// https://github.com/operator-framework/enhancements/blob/master/enhancements/compound-bundle-constraints.md
	p, ok := legacyPredicateParsers[property.Type]
	if !ok {
		return nil, nil
	}
	return p(property.Value)
}

func (pc *predicateConverter) predicateForConstraintProperty(value string) (cache.Predicate, error) {
	constraint, err := constraints.Parse(json.RawMessage([]byte(value)))
	if err != nil {
		return nil, fmt.Errorf("parse olm.constraint: %v", err)
	}

	preds, err := pc.convertConstraints(constraint)
	if err != nil {
		return nil, fmt.Errorf("convert olm.constraint to resolver predicate: %v", err)
	}
	return preds[0], nil
}

// convertConstraints creates predicates from each element of constraints, recursing on compound constraints.
// New constraint types added to the constraints package must be handled here.
func (pc *predicateConverter) convertConstraints(constraints ...constraints.Constraint) ([]cache.Predicate, error) {
	preds := make([]cache.Predicate, len(constraints))
	for i, constraint := range constraints {
		var err error
		switch {
		case constraint.GVK != nil:
			preds[i] = cache.ProvidingAPIPredicate(opregistry.APIKey{
				Group:   constraint.GVK.Group,
				Version: constraint.GVK.Version,
				Kind:    constraint.GVK.Kind,
			})
		case constraint.Package != nil:
			preds[i], err = newPackageRequiredPredicate(constraint.Package.PackageName, constraint.Package.VersionRange)
		case constraint.All != nil:
			subs, perr := pc.convertConstraints(constraint.All.Constraints...)
			preds[i], err = cache.And(subs...), perr
		case constraint.Any != nil:
			subs, perr := pc.convertConstraints(constraint.Any.Constraints...)
			preds[i], err = cache.Or(subs...), perr
		case constraint.Not != nil:
			subs, perr := pc.convertConstraints(constraint.Not.Constraints...)
			preds[i], err = cache.Not(subs...), perr
		case constraint.Cel != nil:
			preds[i], err = cache.CreateCelPredicate(pc.celEnv, constraint.Cel.Rule, constraint.FailureMessage)
		default:
			// Unknown constraint types are handled by constraints.Parse(),
			// but parsed constraints may be empty.
			return nil, fmt.Errorf("constraint is empty")
		}
		if err != nil {
			return nil, err
		}
	}

	return preds, nil
}

var legacyPredicateParsers = map[string]func(string) (cache.Predicate, error){
	"olm.gvk.required":     predicateForRequiredGVKProperty,
	"olm.package.required": predicateForRequiredPackageProperty,
	"olm.label.required":   predicateForRequiredLabelProperty,
}

func predicateForRequiredGVKProperty(value string) (cache.Predicate, error) {
	var gvk struct {
		Group   string `json:"group"`
		Version string `json:"version"`
		Kind    string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(value), &gvk); err != nil {
		return nil, err
	}
	return cache.ProvidingAPIPredicate(opregistry.APIKey{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind,
	}), nil
}

func predicateForRequiredPackageProperty(value string) (cache.Predicate, error) {
	var pkg struct {
		PackageName  string `json:"packageName"`
		VersionRange string `json:"versionRange"`
	}
	if err := json.Unmarshal([]byte(value), &pkg); err != nil {
		return nil, err
	}
	return newPackageRequiredPredicate(pkg.PackageName, pkg.VersionRange)
}

func newPackageRequiredPredicate(name, verRange string) (cache.Predicate, error) {
	ver, err := semver.ParseRange(verRange)
	if err != nil {
		return nil, err
	}
	return cache.And(cache.PkgPredicate(name), cache.VersionInRangePredicate(ver, verRange)), nil
}

func predicateForRequiredLabelProperty(value string) (cache.Predicate, error) {
	var label struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal([]byte(value), &label); err != nil {
		return nil, err
	}
	return cache.LabelPredicate(label.Label), nil
}

func providedAPIsToProperties(apis cache.APISet) ([]*api.Property, error) {
	var out []*api.Property
	for a := range apis {
		val, err := json.Marshal(opregistry.GVKProperty{
			Group:   a.Group,
			Version: a.Version,
			Kind:    a.Kind,
		})
		if err != nil {
			panic(err)
		}
		out = append(out, &api.Property{
			Type:  opregistry.GVKType,
			Value: string(val),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Value < out[j].Value
	})
	return out, nil
}

func requiredAPIsToProperties(apis cache.APISet) ([]*api.Property, error) {
	var out []*api.Property
	for a := range apis {
		val, err := json.Marshal(struct {
			Group   string `json:"group"`
			Version string `json:"version"`
			Kind    string `json:"kind"`
		}{
			Group:   a.Group,
			Version: a.Version,
			Kind:    a.Kind,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, &api.Property{
			Type:  "olm.gvk.required",
			Value: string(val),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Value < out[j].Value
	})
	return out, nil
}
