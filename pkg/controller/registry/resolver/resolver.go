package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	v1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

type OperatorResolver interface {
	SolveOperators(csvs []*v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription, add map[OperatorSourceInfo]struct{}) (OperatorSet, error)
}

type SatResolver struct {
	cache OperatorCacheProvider
	log   logrus.FieldLogger
}

func NewDefaultSatResolver(rcp RegistryClientProvider, catsrcLister v1alpha1listers.CatalogSourceLister, log logrus.FieldLogger) *SatResolver {
	return &SatResolver{
		cache: NewOperatorCache(rcp, log, catsrcLister),
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

func (r *SatResolver) SolveOperators(namespaces []string, csvs []*v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription) (OperatorSet, error) {
	var errs []error

	installables := make(map[solver.Identifier]solver.Installable, 0)
	visited := make(map[OperatorSurface]*BundleInstallable, 0)

	// TODO: better abstraction
	startingCSVs := make(map[string]struct{})

	// build a virtual catalog of all currently installed CSVs
	existingSnapshot, existingInstallables, err := r.newSnapshotForNamespace(namespaces[0], subs, csvs)
	if err != nil {
		return nil, err
	}
	namespacedCache := r.cache.Namespaced(namespaces...).WithExistingOperators(existingSnapshot)

	for _, i := range existingInstallables {
		installables[i.Identifier()] = i
	}

	// build constraints for each Subscription
	for _, sub := range subs {
		pkg := sub.Spec.Package
		catalog := registry.CatalogKey{
			Name:      sub.Spec.CatalogSource,
			Namespace: sub.Spec.CatalogSourceNamespace,
		}
		predicates := []OperatorPredicate{WithPackage(pkg)}
		if sub.Spec.Channel != "" {
			predicates = append(predicates, WithChannel(sub.Spec.Channel))
		}

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
		subInstallables, err := r.getSubscriptionInstallables(pkg, sub.Namespace, current, catalog, predicates, channelFilter, namespacedCache, visited)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, i := range subInstallables {
			installables[i.Identifier()] = i
		}
	}

	r.addInvariants(namespacedCache, installables)

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

func (r *SatResolver) getSubscriptionInstallables(pkg, namespace string, current *Operator, catalog registry.CatalogKey, cachePredicates []OperatorPredicate, channelPredicates []OperatorPredicate, namespacedCache MultiCatalogOperatorFinder, visited map[OperatorSurface]*BundleInstallable) (map[solver.Identifier]solver.Installable, error) {
	installables := make(map[solver.Identifier]solver.Installable, 0)
	candidates := make([]*BundleInstallable, 0)

	subInstallable := NewSubscriptionInstallable(pkg)
	installables[subInstallable.Identifier()] = &subInstallable

	bundles := namespacedCache.Catalog(catalog).Find(cachePredicates...)

	// bundles in the default channel appear first, then lexicographically order by channel name
	sort.SliceStable(bundles, func(i, j int) bool {
		var idef bool
		if isrc := bundles[i].SourceInfo(); isrc != nil {
			idef = isrc.DefaultChannel
		}
		var jdef bool
		if jsrc := bundles[j].SourceInfo(); jsrc != nil {
			jdef = jsrc.DefaultChannel
		}
		if idef == jdef {
			return bundles[i].bundle.ChannelName < bundles[j].bundle.ChannelName
		}
		return idef
	})

	var sortedBundles []*Operator
	lastChannel, lastIndex := "", 0
	for i := 0; i <= len(bundles); i++ {
		if i != len(bundles) && bundles[i].bundle.ChannelName == lastChannel {
			continue
		}
		channel, err := r.sortChannel(bundles[lastIndex:i])
		if err != nil {
			return nil, err
		}
		sortedBundles = append(sortedBundles, channel...)

		if i != len(bundles) {
			lastChannel = bundles[i].bundle.ChannelName
			lastIndex = i
		}
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
			installables[i.Identifier()] = i
		}
	}

	depIds := make([]solver.Identifier, 0)
	for _, c := range candidates {
		// track which operator this is replacing, so that it can be realized when creating the resources on cluster
		if current != nil {
			c.Replaces = current.Identifier()
			// Package name can't be reliably inferred
			// from a CSV without a projected package
			// property, so for the replacement case, a
			// one-to-one conflict is created between the
			// replacer and the replacee. It should be
			// safe to remove this conflict if properties
			// annotations are made mandatory for
			// resolution.
			c.AddConflict(bundleId(current.Identifier(), current.Channel(), registry.NewVirtualCatalogKey(namespace)))
		}
		depIds = append(depIds, c.Identifier())
	}
	if current != nil {
		depIds = append(depIds, bundleId(current.Identifier(), current.Channel(), registry.NewVirtualCatalogKey(namespace)))
	}

	// all candidates added as options for this constraint
	subInstallable.AddDependency(depIds)

	return installables, nil
}

func (r *SatResolver) getBundleInstallables(catalog registry.CatalogKey, predicates []OperatorPredicate, namespacedCache MultiCatalogOperatorFinder, visited map[OperatorSurface]*BundleInstallable) (map[solver.Identifier]struct{}, map[solver.Identifier]*BundleInstallable, error) {
	errs := make([]error, 0)
	installables := make(map[solver.Identifier]*BundleInstallable, 0) // all installables, including dependencies

	var finder OperatorFinder = namespacedCache
	if !catalog.Empty() {
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

		bundleInstallable := NewBundleInstallable(bundle.Identifier(), bundle.Channel(), bundleSource.Catalog)
		visited[bundle] = &bundleInstallable

		dependencyPredicates, err := bundle.DependencyPredicates()
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, d := range dependencyPredicates {
			// errors ignored; this will build an empty/unsatisfiable dependency if no candidates are found
			candidateBundles, _ := AtLeast(1, namespacedCache.FindPreferred(&bundle.sourceInfo.Catalog, d))
			sortedBundles, err := r.sortBundles(candidateBundles)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			bundleDependencies := make([]solver.Identifier, 0)
			for _, dep := range sortedBundles {
				found := namespacedCache.Catalog(dep.SourceInfo().Catalog).Find(WithCSVName(dep.Identifier()))
				if len(found) == 0 {
					err := fmt.Errorf("couldn't find %s in %s", bundle.Identifier(), dep.sourceInfo.Catalog)
					errs = append(errs, err)
					r.log.Warnf("cache consistency error: %s not found in %s", bundle.Identifier(), dep.sourceInfo.Catalog)
					continue
				}
				b := found[0]
				src := b.SourceInfo()
				if src == nil {
					err := fmt.Errorf("unable to resolve the source of bundle %s, invalid cache", bundle.Identifier())
					errs = append(errs, err)
					continue
				}

				i := NewBundleInstallable(b.Identifier(), b.Channel(), src.Catalog)
				installables[i.Identifier()] = &i
				bundleDependencies = append(bundleDependencies, i.Identifier())
				bundleStack = append(bundleStack, b)
			}

			bundleInstallable.AddDependency(bundleDependencies)
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

func (r *SatResolver) newSnapshotForNamespace(namespace string, subs []*v1alpha1.Subscription, csvs []*v1alpha1.ClusterServiceVersion) (*CatalogSnapshot, []solver.Installable, error) {
	installables := make([]solver.Installable, 0)
	existingOperatorCatalog := registry.NewVirtualCatalogKey(namespace)
	// build a catalog snapshot of CSVs without subscriptions
	csvsWithSubscriptions := make(map[*v1alpha1.ClusterServiceVersion]struct{})
	for _, sub := range subs {
		for _, csv := range csvs {
			if csv.Name == sub.Status.InstalledCSV {
				csvsWithSubscriptions[csv] = struct{}{}
				break
			}
		}
	}
	var csvsMissingProperties []*v1alpha1.ClusterServiceVersion
	standaloneOperators := make([]*Operator, 0)
	for _, csv := range csvs {
		var constraints []solver.Constraint
		if _, ok := csvsWithSubscriptions[csv]; !ok {
			// CSVs already associated with a Subscription
			// may be replaced, but freestanding CSVs must
			// appear in any solution.
			constraints = append(constraints, solver.Mandatory())
		}

		op, err := NewOperatorFromV1Alpha1CSV(csv)
		if err != nil {
			return nil, nil, err
		}

		if anno, ok := csv.GetAnnotations()[projection.PropertiesAnnotationKey]; !ok {
			csvsMissingProperties = append(csvsMissingProperties, csv)
		} else if props, err := projection.PropertyListFromPropertiesAnnotation(anno); err != nil {
			return nil, nil, fmt.Errorf("failed to retrieve properties of csv %q: %w", csv.GetName(), err)
		} else {
			op.properties = props
		}

		op.sourceInfo = &OperatorSourceInfo{
			Catalog: existingOperatorCatalog,
		}
		standaloneOperators = append(standaloneOperators, op)

		// all standalone operators are mandatory
		i := NewBundleInstallable(op.Identifier(), "", existingOperatorCatalog, constraints...)
		installables = append(installables, &i)
	}

	if len(csvsMissingProperties) > 0 {
		names := make([]string, len(csvsMissingProperties))
		for i, csv := range csvsMissingProperties {
			names[i] = csv.GetName()
		}
		r.log.Infof("considered csvs without properties annotation during resolution: %v", names)
	}

	return NewRunningOperatorSnapshot(r.log, existingOperatorCatalog, standaloneOperators), installables, nil
}

func (r *SatResolver) addInvariants(namespacedCache MultiCatalogOperatorFinder, installables map[solver.Identifier]solver.Installable) {
	// no two operators may provide the same GVK or Package in a namespace
	conflictToInstallable := make(map[string][]solver.Installable)
	for _, installable := range installables {
		bundleInstallable, ok := installable.(*BundleInstallable)
		if !ok {
			continue
		}
		csvName, channel, catalog, err := bundleInstallable.BundleSourceInfo()
		if err != nil {
			continue
		}

		op, err := ExactlyOne(namespacedCache.Catalog(catalog).Find(WithCSVName(csvName), WithChannel(channel)))
		if err != nil {
			continue
		}

		// cannot provide the same GVK
		for _, p := range op.Properties() {
			if p.Type != opregistry.GVKType {
				continue
			}
			var prop opregistry.GVKProperty
			err := json.Unmarshal([]byte(p.Value), &prop)
			if err != nil {
				continue
			}
			key := fmt.Sprintf("gvkunique/%s/%s/%s", prop.Group, prop.Version, prop.Kind)
			_, ok := conflictToInstallable[key]
			if !ok {
				conflictToInstallable[key] = make([]solver.Installable, 0)
			}
			conflictToInstallable[key] = append(conflictToInstallable[key], installable)
		}

		// cannot have the same package
		for _, p := range op.Properties() {
			if p.Type != opregistry.PackageType {
				continue
			}
			var prop opregistry.PackageProperty
			err := json.Unmarshal([]byte(p.Value), &prop)
			if err != nil {
				continue
			}
			key := fmt.Sprintf("pkgunique/%s", prop.PackageName)
			_, ok := conflictToInstallable[key]
			if !ok {
				conflictToInstallable[key] = make([]solver.Installable, 0)
			}
			conflictToInstallable[key] = append(conflictToInstallable[key], installable)
		}
	}

	for key, is := range conflictToInstallable {
		if len(is) <= 1 {
			continue
		}

		ids := make([]solver.Identifier, 0)
		for _, d := range is {
			ids = append(ids, d.Identifier())
		}
		s := NewSubscriptionInstallable(key)
		s.constraints = append(s.constraints, solver.AtMost(1, ids...))
		installables[s.Identifier()] = &s
	}
}

func (r *SatResolver) sortBundles(bundles []*Operator) ([]*Operator, error) {
	// assume bundles have been passed in sorted by catalog already
	catalogOrder := make([]registry.CatalogKey, 0)

	type PackageChannel struct {
		Package, Channel string
		DefaultChannel   bool
	}
	// TODO: for now channels will be sorted lexicographically
	channelOrder := make(map[registry.CatalogKey][]PackageChannel)

	// partition by catalog -> channel -> bundle
	partitionedBundles := map[registry.CatalogKey]map[PackageChannel][]*Operator{}
	for _, b := range bundles {
		pc := PackageChannel{
			Package:        b.Package(),
			Channel:        b.Channel(),
			DefaultChannel: b.SourceInfo().DefaultChannel,
		}
		if _, ok := partitionedBundles[b.sourceInfo.Catalog]; !ok {
			catalogOrder = append(catalogOrder, b.sourceInfo.Catalog)
			partitionedBundles[b.sourceInfo.Catalog] = make(map[PackageChannel][]*Operator)
		}
		if _, ok := partitionedBundles[b.sourceInfo.Catalog][pc]; !ok {
			channelOrder[b.sourceInfo.Catalog] = append(channelOrder[b.sourceInfo.Catalog], pc)
			partitionedBundles[b.sourceInfo.Catalog][pc] = make([]*Operator, 0)
		}
		partitionedBundles[b.sourceInfo.Catalog][pc] = append(partitionedBundles[b.sourceInfo.Catalog][pc], b)
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
			sorted, err := r.sortChannel(partitionedBundles[catalog][channel])
			if err != nil {
				return nil, err
			}
			partitionedBundles[catalog][channel] = sorted
		}
	}
	all := make([]*Operator, 0)
	for _, catalog := range catalogOrder {
		for _, channel := range channelOrder[catalog] {
			all = append(all, partitionedBundles[catalog][channel]...)
		}
	}
	return all, nil
}

// sorts bundle in a channel by replaces
func (r *SatResolver) sortChannel(bundles []*Operator) ([]*Operator, error) {
	if len(bundles) <= 1 {
		return bundles, nil
	}

	channel := []*Operator{}

	bundleLookup := map[string]*Operator{}

	// if a replaces b, then replacedBy[b] = a
	replacedBy := map[*Operator]*Operator{}
	replaces := map[*Operator]*Operator{}
	skipped := map[string]*Operator{}

	for _, b := range bundles {
		bundleLookup[b.Identifier()] = b
	}

	for _, b := range bundles {
		if b.replaces != "" {
			if r, ok := bundleLookup[b.replaces]; ok {
				replacedBy[r] = b
				replaces[b] = r
			}
		}
		for _, skip := range b.skips {
			if r, ok := bundleLookup[skip]; ok {
				replacedBy[r] = b
				skipped[skip] = r
			}
		}
	}

	// a bundle without a replacement is a channel head, but if we find more than one of those something is weird
	headCandidates := []*Operator{}
	for _, b := range bundles {
		if _, ok := replacedBy[b]; !ok {
			headCandidates = append(headCandidates, b)
		}
	}

	if len(headCandidates) > 1 {
		var names []string
		for _, v := range headCandidates {
			names = append(names, v.Identifier())
		}
		return nil, fmt.Errorf("found multiple channel heads: %v, please check the `replaces`/`skipRange` fields of the operator bundles", names)

	} else if len(headCandidates) < 1 {
		return nil, fmt.Errorf("head of channel not found")
	}

	head := headCandidates[0]
	current := head
	skip := false
	for {
		if skip == false {
			channel = append(channel, current)
		}
		skip = false
		next, ok := replaces[current]
		if !ok {
			break
		}
		if _, ok := skipped[current.Identifier()]; ok {
			skip = true
		}
		current = next
	}

	// TODO: do we care if the channel doesn't include every bundle in the input?

	return channel, nil
}
