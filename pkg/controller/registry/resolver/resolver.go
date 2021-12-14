package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/api/pkg/constraints"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	v1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

type OperatorResolver interface {
	SolveOperators(csvs []*v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription, add map[cache.OperatorSourceInfo]struct{}) (cache.OperatorSet, error)
}

type SatResolver struct {
	cache cache.OperatorCacheProvider
	log   logrus.FieldLogger
	pc    *predicateConverter
}

func NewDefaultSatResolver(rcp cache.SourceProvider, catsrcLister v1alpha1listers.CatalogSourceLister, logger logrus.FieldLogger) *SatResolver {
	return &SatResolver{
		cache: cache.New(rcp, cache.WithLogger(logger), cache.WithCatalogSourceLister(catsrcLister)),
		log:   logger,
		pc:    &predicateConverter{},
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

func (r *SatResolver) SolveOperators(namespaces []string, csvs []*v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription) (cache.OperatorSet, error) {
	var errs []error

	installables := make(map[solver.Identifier]solver.Installable, 0)
	visited := make(map[*cache.Entry]*BundleInstallable, 0)

	// TODO: better abstraction
	startingCSVs := make(map[string]struct{})

	// build a virtual catalog of all currently installed CSVs
	existingSnapshot, err := r.newSnapshotForNamespace(namespaces[0], subs, csvs)
	if err != nil {
		return nil, err
	}
	namespacedCache := r.cache.Namespaced(namespaces...).WithExistingOperators(existingSnapshot, namespaces[0])

	_, existingInstallables, err := r.getBundleInstallables(namespaces[0], cache.Filter(existingSnapshot.Entries, cache.True()), namespacedCache, visited)
	if err != nil {
		return nil, err
	}
	for _, i := range existingInstallables {
		installables[i.Identifier()] = i
	}

	// build constraints for each Subscription
	for _, sub := range subs {
		// find the currently installed operator (if it exists)
		var current *cache.Entry
		for _, csv := range csvs {
			if csv.Name == sub.Status.InstalledCSV {
				op, err := newOperatorFromV1Alpha1CSV(csv)
				if err != nil {
					return nil, err
				}
				current = op
				break
			}
		}

		if current == nil && sub.Spec.StartingCSV != "" {
			startingCSVs[sub.Spec.StartingCSV] = struct{}{}
		}

		// find operators, in channel order, that can skip from the current version or list the current in "replaces"
		subInstallables, err := r.getSubscriptionInstallables(sub, current, namespacedCache, visited)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, i := range subInstallables {
			installables[i.Identifier()] = i
		}
	}

	r.addInvariants(namespacedCache, installables)

	if err := namespacedCache.Error(); err != nil {
		return nil, err
	}

	input := make([]solver.Installable, 0)
	for _, i := range installables {
		input = append(input, i)
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	s, err := solver.New(solver.WithInput(input), solver.WithTracer(solver.LoggingTracer{Writer: &debugWriter{r.log}}))
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
		if bundleInstallable, ok := installable.(*BundleInstallable); ok {
			_, _, catalog, err := bundleInstallable.BundleSourceInfo()
			if err != nil {
				return nil, fmt.Errorf("error determining origin of operator: %w", err)
			}
			if catalog.Virtual() {
				// Result is expected to contain only new things.
				continue
			}
			operatorInstallables = append(operatorInstallables, *bundleInstallable)
		}
	}

	operators := make(map[string]*cache.Entry, 0)
	for _, installableOperator := range operatorInstallables {
		csvName, channel, catalog, err := installableOperator.BundleSourceInfo()
		if err != nil {
			errs = append(errs, err)
			continue
		}

		op, err := cache.ExactlyOne(namespacedCache.Catalog(catalog).Find(cache.CSVNamePredicate(csvName), cache.ChannelPredicate(channel)))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(installableOperator.Replaces) > 0 {
			op.Replaces = installableOperator.Replaces // TODO: Don't mutate object from cache!
		}

		// lookup if this installable came from a starting CSV
		if _, ok := startingCSVs[csvName]; ok {
			op.SourceInfo.StartingCSV = csvName // TODO: Don't mutate object from cache!
		}

		operators[csvName] = op
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	return operators, nil
}

func (r *SatResolver) getSubscriptionInstallables(sub *v1alpha1.Subscription, current *cache.Entry, namespacedCache cache.MultiCatalogOperatorFinder, visited map[*cache.Entry]*BundleInstallable) (map[solver.Identifier]solver.Installable, error) {
	var cachePredicates, channelPredicates []cache.Predicate
	installables := make(map[solver.Identifier]solver.Installable, 0)

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

		var si solver.Installable
		switch {
		case nall == 0:
			si = NewInvalidSubscriptionInstallable(sub.GetName(), fmt.Sprintf("no operators found from catalog %s in namespace %s referenced by subscription %s", sub.Spec.CatalogSource, sub.Spec.CatalogSourceNamespace, sub.GetName()))
		case npkg == 0:
			si = NewInvalidSubscriptionInstallable(sub.GetName(), fmt.Sprintf("no operators found in package %s in the catalog referenced by subscription %s", sub.Spec.Package, sub.GetName()))
		case nch == 0:
			si = NewInvalidSubscriptionInstallable(sub.GetName(), fmt.Sprintf("no operators found in channel %s of package %s in the catalog referenced by subscription %s", sub.Spec.Channel, sub.Spec.Package, sub.GetName()))
		case ncsv == 0:
			si = NewInvalidSubscriptionInstallable(sub.GetName(), fmt.Sprintf("no operators found with name %s in channel %s of package %s in the catalog referenced by subscription %s", sub.Spec.StartingCSV, sub.Spec.Channel, sub.Spec.Package, sub.GetName()))
		}

		if si != nil {
			installables[si.Identifier()] = si
			return installables, nil
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

	candidates := make([]*BundleInstallable, 0)
	for _, o := range cache.Filter(sortedBundles, channelPredicates...) {
		predicates := append(cachePredicates, cache.CSVNamePredicate(o.Name))
		stack := namespacedCache.Catalog(catalog).Find(predicates...)
		id, installable, err := r.getBundleInstallables(sub.Namespace, stack, namespacedCache, visited)
		if err != nil {
			return nil, err
		}
		if len(id) < 1 {
			return nil, fmt.Errorf("could not find any potential bundles for subscription: %s", sub.Spec.Package)
		}

		for _, i := range installable {
			if _, ok := id[i.Identifier()]; ok {
				candidates = append(candidates, i)
			}
			installables[i.Identifier()] = i
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
			c.AddConflict(bundleId(current.Name, current.Channel(), cache.NewVirtualSourceKey(sub.GetNamespace())))
		}
		depIds = append(depIds, c.Identifier())
	}
	if current != nil {
		depIds = append(depIds, bundleId(current.Name, current.Channel(), cache.NewVirtualSourceKey(sub.GetNamespace())))
	}

	// all candidates added as options for this constraint
	subInstallable := NewSubscriptionInstallable(sub.GetName(), depIds)
	installables[subInstallable.Identifier()] = subInstallable

	return installables, nil
}

func (r *SatResolver) getBundleInstallables(preferredNamespace string, bundleStack []*cache.Entry, namespacedCache cache.MultiCatalogOperatorFinder, visited map[*cache.Entry]*BundleInstallable) (map[solver.Identifier]struct{}, map[solver.Identifier]*BundleInstallable, error) {
	errs := make([]error, 0)
	installables := make(map[solver.Identifier]*BundleInstallable, 0) // all installables, including dependencies

	// track the first layer of installable ids
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
			installables[b.identifier] = b
			continue
		}

		bundleInstallable, err := NewBundleInstallableFromOperator(bundle)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		visited[bundle] = &bundleInstallable

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
				i, err := NewBundleInstallableFromOperator(b)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				installables[i.Identifier()] = &i
				bundleDependencies = append(bundleDependencies, i.Identifier())
				bundleStack = append(bundleStack, b)
			}
			bundleInstallable.AddConstraint(PrettyConstraint(
				solver.Dependency(bundleDependencies...),
				fmt.Sprintf("bundle %s requires an operator %s", bundle.Name, d.String()),
			))
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

func (r *SatResolver) inferProperties(csv *v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription) ([]*api.Property, error) {
	var properties []*api.Property

	packages := make(map[string]struct{})
	for _, sub := range subs {
		if sub.Status.InstalledCSV != csv.Name {
			continue
		}
		// Without sanity checking the Subscription spec's
		// package against catalog contents, updates to the
		// Subscription spec could result in a bad package
		// inference.
		for _, entry := range r.cache.Namespaced(sub.Spec.CatalogSourceNamespace).Catalog(cache.SourceKey{Namespace: sub.Spec.CatalogSourceNamespace, Name: sub.Spec.CatalogSource}).Find(cache.And(cache.CSVNamePredicate(csv.Name), cache.PkgPredicate(sub.Spec.Package))) {
			if pkg := entry.Package(); pkg != "" {
				packages[pkg] = struct{}{}
			}
		}
	}
	if l := len(packages); l != 1 {
		r.log.Warnf("could not unambiguously infer package name for %q (found %d distinct package names)", csv.Name, l)
		return properties, nil
	}
	var pkg string
	for pkg = range packages {
		// Assign the single key to pkg.
	}
	var version string // Emit empty string rather than "0.0.0" if .spec.version is zero-valued.
	if !csv.Spec.Version.Version.Equals(semver.Version{}) {
		version = csv.Spec.Version.String()
	}
	if pp, err := json.Marshal(opregistry.PackageProperty{
		PackageName: pkg,
		Version:     version,
	}); err != nil {
		return nil, fmt.Errorf("failed to marshal inferred package property: %w", err)
	} else {
		properties = append(properties, &api.Property{
			Type:  opregistry.PackageType,
			Value: string(pp),
		})
	}

	return properties, nil
}

func (r *SatResolver) newSnapshotForNamespace(namespace string, subs []*v1alpha1.Subscription, csvs []*v1alpha1.ClusterServiceVersion) (*cache.Snapshot, error) {
	existingOperatorCatalog := cache.NewVirtualSourceKey(namespace)
	// build a catalog snapshot of CSVs without subscriptions
	csvSubscriptions := make(map[*v1alpha1.ClusterServiceVersion]*v1alpha1.Subscription)
	for _, sub := range subs {
		for _, csv := range csvs {
			if csv.Name == sub.Status.InstalledCSV {
				csvSubscriptions[csv] = sub
				break
			}
		}
	}
	var csvsMissingProperties []*v1alpha1.ClusterServiceVersion
	standaloneOperators := make([]*cache.Entry, 0)
	for _, csv := range csvs {
		op, err := newOperatorFromV1Alpha1CSV(csv)
		if err != nil {
			return nil, err
		}

		if anno, ok := csv.GetAnnotations()[projection.PropertiesAnnotationKey]; !ok {
			csvsMissingProperties = append(csvsMissingProperties, csv)
			if inferred, err := r.inferProperties(csv, subs); err != nil {
				r.log.Warnf("unable to infer properties for csv %q: %w", csv.Name, err)
			} else {
				op.Properties = append(op.Properties, inferred...)
			}
		} else if props, err := projection.PropertyListFromPropertiesAnnotation(anno); err != nil {
			return nil, fmt.Errorf("failed to retrieve properties of csv %q: %w", csv.GetName(), err)
		} else {
			op.Properties = props
		}

		op.SourceInfo = &cache.OperatorSourceInfo{
			Catalog:      existingOperatorCatalog,
			Subscription: csvSubscriptions[csv],
		}
		// Try to determine source package name from properties and add to SourceInfo.
		for _, p := range op.Properties {
			if p.Type != opregistry.PackageType {
				continue
			}
			var pp opregistry.PackageProperty
			err := json.Unmarshal([]byte(p.Value), &pp)
			if err != nil {
				r.log.Warnf("failed to unmarshal package property of csv %q: %w", csv.Name, err)
				continue
			}
			op.SourceInfo.Package = pp.PackageName
		}

		standaloneOperators = append(standaloneOperators, op)
	}

	if len(csvsMissingProperties) > 0 {
		names := make([]string, len(csvsMissingProperties))
		for i, csv := range csvsMissingProperties {
			names[i] = csv.GetName()
		}
		r.log.Infof("considered csvs without properties annotation during resolution: %v", names)
	}

	return &cache.Snapshot{Entries: standaloneOperators}, nil
}

func (r *SatResolver) addInvariants(namespacedCache cache.MultiCatalogOperatorFinder, installables map[solver.Identifier]solver.Installable) {
	// no two operators may provide the same GVK or Package in a namespace
	gvkConflictToInstallable := make(map[opregistry.GVKProperty][]solver.Identifier)
	packageConflictToInstallable := make(map[string][]solver.Identifier)
	for _, installable := range installables {
		bundleInstallable, ok := installable.(*BundleInstallable)
		if !ok {
			continue
		}
		csvName, channel, catalog, err := bundleInstallable.BundleSourceInfo()
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
			gvkConflictToInstallable[prop] = append(gvkConflictToInstallable[prop], installable.Identifier())
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
			packageConflictToInstallable[prop.PackageName] = append(packageConflictToInstallable[prop.PackageName], installable.Identifier())
		}
	}

	for gvk, is := range gvkConflictToInstallable {
		s := NewSingleAPIProviderInstallable(gvk.Group, gvk.Version, gvk.Kind, is)
		installables[s.Identifier()] = s
	}

	for pkg, is := range packageConflictToInstallable {
		s := NewSinglePackageInstanceInstallable(pkg, is)
		installables[s.Identifier()] = s
	}
}

func (r *SatResolver) sortBundles(bundles []*cache.Entry) ([]*cache.Entry, error) {
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
type predicateConverter struct{}

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
		case constraint.None != nil:
			subs, perr := pc.convertConstraints(constraint.None.Constraints...)
			preds[i], err = cache.Not(subs...), perr
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

func newOperatorFromV1Alpha1CSV(csv *v1alpha1.ClusterServiceVersion) (*cache.Entry, error) {
	providedAPIs := cache.EmptyAPISet()
	for _, crdDef := range csv.Spec.CustomResourceDefinitions.Owned {
		parts := strings.SplitN(crdDef.Name, ".", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("error parsing crd name: %s", crdDef.Name)
		}
		providedAPIs[opregistry.APIKey{Plural: parts[0], Group: parts[1], Version: crdDef.Version, Kind: crdDef.Kind}] = struct{}{}
	}
	for _, api := range csv.Spec.APIServiceDefinitions.Owned {
		providedAPIs[opregistry.APIKey{Group: api.Group, Version: api.Version, Kind: api.Kind, Plural: api.Name}] = struct{}{}
	}

	requiredAPIs := cache.EmptyAPISet()
	for _, crdDef := range csv.Spec.CustomResourceDefinitions.Required {
		parts := strings.SplitN(crdDef.Name, ".", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("error parsing crd name: %s", crdDef.Name)
		}
		requiredAPIs[opregistry.APIKey{Plural: parts[0], Group: parts[1], Version: crdDef.Version, Kind: crdDef.Kind}] = struct{}{}
	}
	for _, api := range csv.Spec.APIServiceDefinitions.Required {
		requiredAPIs[opregistry.APIKey{Group: api.Group, Version: api.Version, Kind: api.Kind, Plural: api.Name}] = struct{}{}
	}

	properties, err := providedAPIsToProperties(providedAPIs)
	if err != nil {
		return nil, err
	}
	dependencies, err := requiredAPIsToProperties(requiredAPIs)
	if err != nil {
		return nil, err
	}
	properties = append(properties, dependencies...)

	return &cache.Entry{
		Name:         csv.GetName(),
		Version:      &csv.Spec.Version.Version,
		ProvidedAPIs: providedAPIs,
		RequiredAPIs: requiredAPIs,
		SourceInfo:   &cache.ExistingOperator,
		Properties:   properties,
	}, nil
}

func providedAPIsToProperties(apis cache.APISet) (out []*api.Property, err error) {
	out = make([]*api.Property, 0)
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
	if len(out) > 0 {
		return
	}
	return nil, nil
}

func requiredAPIsToProperties(apis cache.APISet) (out []*api.Property, err error) {
	if len(apis) == 0 {
		return
	}
	out = make([]*api.Property, 0)
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
	if len(out) > 0 {
		return
	}
	return nil, nil
}
