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

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	v1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
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
}

func NewDefaultSatResolver(rcp cache.RegistryClientProvider, catsrcLister v1alpha1listers.CatalogSourceLister, log logrus.FieldLogger) *SatResolver {
	return &SatResolver{
		cache: cache.NewOperatorCache(rcp, log, catsrcLister),
		log:   log,
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
	visited := make(map[*cache.Operator]*BundleInstallable, 0)

	// TODO: better abstraction
	startingCSVs := make(map[string]struct{})

	// build a virtual catalog of all currently installed CSVs
	existingSnapshot, err := r.newSnapshotForNamespace(namespaces[0], subs, csvs)
	if err != nil {
		return nil, err
	}
	namespacedCache := r.cache.Namespaced(namespaces...).WithExistingOperators(existingSnapshot)

	_, existingInstallables, err := r.getBundleInstallables(registry.NewVirtualCatalogKey(namespaces[0]), existingSnapshot.Find(), namespacedCache, visited)
	if err != nil {
		return nil, err
	}
	for _, i := range existingInstallables {
		installables[i.Identifier()] = i
	}

	// build constraints for each Subscription
	for _, sub := range subs {
		// find the currently installed operator (if it exists)
		var current *cache.Operator
		for _, csv := range csvs {
			if csv.Name == sub.Status.InstalledCSV {
				op, err := cache.NewOperatorFromV1Alpha1CSV(csv)
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

	operators := make(map[string]*cache.Operator, 0)
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

func (r *SatResolver) getSubscriptionInstallables(sub *v1alpha1.Subscription, current *cache.Operator, namespacedCache cache.MultiCatalogOperatorFinder, visited map[*cache.Operator]*BundleInstallable) (map[solver.Identifier]solver.Installable, error) {
	var cachePredicates, channelPredicates []cache.OperatorPredicate
	installables := make(map[solver.Identifier]solver.Installable, 0)

	catalog := registry.CatalogKey{
		Name:      sub.Spec.CatalogSource,
		Namespace: sub.Spec.CatalogSourceNamespace,
	}

	var bundles []*cache.Operator
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
		bundles = namespacedCache.Catalog(catalog).Find(cachePredicates...)

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

	// bundles in the default channel appear first, then lexicographically order by channel name
	sort.SliceStable(bundles, func(i, j int) bool {
		var idef bool
		if isrc := bundles[i].SourceInfo; isrc != nil {
			idef = isrc.DefaultChannel
		}
		var jdef bool
		if jsrc := bundles[j].SourceInfo; jsrc != nil {
			jdef = jsrc.DefaultChannel
		}
		if idef == jdef {
			return bundles[i].Bundle.ChannelName < bundles[j].Bundle.ChannelName
		}
		return idef
	})

	var sortedBundles []*cache.Operator
	lastChannel, lastIndex := "", 0
	for i := 0; i <= len(bundles); i++ {
		if i != len(bundles) && bundles[i].Bundle.ChannelName == lastChannel {
			continue
		}
		channel, err := sortChannel(bundles[lastIndex:i])
		if err != nil {
			return nil, err
		}
		sortedBundles = append(sortedBundles, channel...)

		if i != len(bundles) {
			lastChannel = bundles[i].Bundle.ChannelName
			lastIndex = i
		}
	}

	candidates := make([]*BundleInstallable, 0)
	for _, o := range cache.Filter(sortedBundles, channelPredicates...) {
		predicates := append(cachePredicates, cache.CSVNamePredicate(o.Name))
		stack := namespacedCache.Catalog(catalog).Find(predicates...)
		id, installable, err := r.getBundleInstallables(catalog, stack, namespacedCache, visited)
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
			c.AddConflict(bundleId(current.Name, current.Channel(), registry.NewVirtualCatalogKey(sub.GetNamespace())))
		}
		depIds = append(depIds, c.Identifier())
	}
	if current != nil {
		depIds = append(depIds, bundleId(current.Name, current.Channel(), registry.NewVirtualCatalogKey(sub.GetNamespace())))
	}

	// all candidates added as options for this constraint
	subInstallable := NewSubscriptionInstallable(sub.GetName(), depIds)
	installables[subInstallable.Identifier()] = subInstallable

	return installables, nil
}

func (r *SatResolver) getBundleInstallables(catalog registry.CatalogKey, bundleStack []*cache.Operator, namespacedCache cache.MultiCatalogOperatorFinder, visited map[*cache.Operator]*BundleInstallable) (map[solver.Identifier]struct{}, map[solver.Identifier]*BundleInstallable, error) {
	errs := make([]error, 0)
	installables := make(map[solver.Identifier]*BundleInstallable, 0) // all installables, including dependencies

	// track the first layer of installable ids
	var initial = make(map[*cache.Operator]struct{})
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

		dependencyPredicates, err := bundle.DependencyPredicates()
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
			sortedBundles, err := r.sortBundles(namespacedCache.FindPreferred(&bundle.SourceInfo.Catalog, sourcePredicate))
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
		for _, entry := range r.cache.Namespaced(sub.Namespace).Catalog(registry.CatalogKey{Namespace: sub.Spec.CatalogSourceNamespace, Name: sub.Spec.CatalogSource}).Find(cache.And(cache.CSVNamePredicate(csv.Name), cache.PkgPredicate(sub.Spec.Package))) {
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

func (r *SatResolver) newSnapshotForNamespace(namespace string, subs []*v1alpha1.Subscription, csvs []*v1alpha1.ClusterServiceVersion) (*cache.CatalogSnapshot, error) {
	existingOperatorCatalog := registry.NewVirtualCatalogKey(namespace)
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
	standaloneOperators := make([]*cache.Operator, 0)
	for _, csv := range csvs {
		op, err := cache.NewOperatorFromV1Alpha1CSV(csv)
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

	return cache.NewRunningOperatorSnapshot(r.log, existingOperatorCatalog, standaloneOperators), nil
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

func (r *SatResolver) sortBundles(bundles []*cache.Operator) ([]*cache.Operator, error) {
	// assume bundles have been passed in sorted by catalog already
	catalogOrder := make([]registry.CatalogKey, 0)

	type PackageChannel struct {
		Package, Channel string
		DefaultChannel   bool
	}
	// TODO: for now channels will be sorted lexicographically
	channelOrder := make(map[registry.CatalogKey][]PackageChannel)

	// partition by catalog -> channel -> bundle
	partitionedBundles := map[registry.CatalogKey]map[PackageChannel][]*cache.Operator{}
	for _, b := range bundles {
		pc := PackageChannel{
			Package:        b.Package(),
			Channel:        b.Channel(),
			DefaultChannel: b.SourceInfo.DefaultChannel,
		}
		if _, ok := partitionedBundles[b.SourceInfo.Catalog]; !ok {
			catalogOrder = append(catalogOrder, b.SourceInfo.Catalog)
			partitionedBundles[b.SourceInfo.Catalog] = make(map[PackageChannel][]*cache.Operator)
		}
		if _, ok := partitionedBundles[b.SourceInfo.Catalog][pc]; !ok {
			channelOrder[b.SourceInfo.Catalog] = append(channelOrder[b.SourceInfo.Catalog], pc)
			partitionedBundles[b.SourceInfo.Catalog][pc] = make([]*cache.Operator, 0)
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
	all := make([]*cache.Operator, 0)
	for _, catalog := range catalogOrder {
		for _, channel := range channelOrder[catalog] {
			all = append(all, partitionedBundles[catalog][channel]...)
		}
	}
	return all, nil
}

// Sorts bundle in a channel by replaces. All entries in the argument
// are assumed to have the same Package and Channel.
func sortChannel(bundles []*cache.Operator) ([]*cache.Operator, error) {
	if len(bundles) < 1 {
		return bundles, nil
	}

	packageName := bundles[0].Package()
	channelName := bundles[0].Channel()

	bundleLookup := map[string]*cache.Operator{}

	// if a replaces b, then replacedBy[b] = a
	replacedBy := map[*cache.Operator]*cache.Operator{}
	replaces := map[*cache.Operator]*cache.Operator{}
	skipped := map[string]*cache.Operator{}

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
	headCandidates := []*cache.Operator{}
	for _, b := range bundles {
		if _, ok := replacedBy[b]; !ok {
			headCandidates = append(headCandidates, b)
		}
	}
	if len(headCandidates) == 0 {
		return nil, fmt.Errorf("no channel heads (entries not replaced by another entry) found in channel %q of package %q", channelName, packageName)
	}

	var chains [][]*cache.Operator
	for _, head := range headCandidates {
		var chain []*cache.Operator
		visited := make(map[*cache.Operator]struct{})
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
