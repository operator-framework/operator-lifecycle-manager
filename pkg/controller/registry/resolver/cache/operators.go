package cache

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

type APISet map[opregistry.APIKey]struct{}

func EmptyAPISet() APISet {
	return map[opregistry.APIKey]struct{}{}
}

func (s APISet) PopAPIKey() *opregistry.APIKey {
	for a := range s {
		api := &opregistry.APIKey{
			Group:   a.Group,
			Version: a.Version,
			Kind:    a.Kind,
			Plural:  a.Plural,
		}
		delete(s, a)
		return api
	}
	return nil
}

func GVKStringToProvidedAPISet(gvksStr string) APISet {
	set := make(APISet)
	// TODO: Should we make gvk strings lowercase to avoid issues with user set gvks?
	gvks := strings.Split(strings.Replace(gvksStr, " ", "", -1), ",")
	for _, gvkStr := range gvks {
		gvk, _ := schema.ParseKindArg(gvkStr)
		if gvk != nil {
			set[opregistry.APIKey{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind}] = struct{}{}
		}
	}

	return set
}

func APIKeyToGVKString(key opregistry.APIKey) string {
	// TODO: Add better validation of GVK
	return strings.Join([]string{key.Kind, key.Version, key.Group}, ".")
}

func APIKeyToGVKHash(key opregistry.APIKey) (string, error) {
	hash := fnv.New64a()
	if _, err := hash.Write([]byte(APIKeyToGVKString(key))); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum64()), nil
}

func (s APISet) String() string {
	gvkStrs := make([]string, len(s))
	i := 0
	for api := range s {
		// TODO: Only add valid GVK strings
		gvkStrs[i] = APIKeyToGVKString(api)
		i++
	}
	sort.Strings(gvkStrs)

	return strings.Join(gvkStrs, ",")
}

// Union returns the union of the APISet and the given list of APISets
func (s APISet) Union(sets ...APISet) APISet {
	union := make(APISet)
	for api := range s {
		union[api] = struct{}{}
	}
	for _, set := range sets {
		for api := range set {
			union[api] = struct{}{}
		}
	}

	return union
}

// Intersection returns the intersection of the APISet and the given list of APISets
func (s APISet) Intersection(sets ...APISet) APISet {
	intersection := make(APISet)
	for _, set := range sets {
		for api := range set {
			if _, ok := s[api]; ok {
				intersection[api] = struct{}{}
			}
		}
	}

	return intersection
}

func (s APISet) Difference(set APISet) APISet {
	difference := make(APISet).Union(s)
	for api := range set {
		if _, ok := difference[api]; ok {
			delete(difference, api)
		}
	}

	return difference
}

// IsSubset returns true if the APISet is a subset of the given one
func (s APISet) IsSubset(set APISet) bool {
	for api := range s {
		if _, ok := set[api]; !ok {
			return false
		}
	}

	return true
}

// StripPlural returns the APISet with the Plural field of all APIKeys removed
func (s APISet) StripPlural() APISet {
	set := make(APISet)
	for api := range s {
		set[opregistry.APIKey{Group: api.Group, Version: api.Version, Kind: api.Kind}] = struct{}{}
	}

	return set
}

type APIOwnerSet map[opregistry.APIKey]OperatorSurface

func EmptyAPIOwnerSet() APIOwnerSet {
	return map[opregistry.APIKey]OperatorSurface{}
}

type OperatorSet map[string]OperatorSurface

func EmptyOperatorSet() OperatorSet {
	return map[string]OperatorSurface{}
}

// Snapshot returns a new set, pointing to the same values
func (o OperatorSet) Snapshot() OperatorSet {
	out := make(map[string]OperatorSurface)
	for key, val := range o {
		out[key] = val
	}
	return out
}

type APIMultiOwnerSet map[opregistry.APIKey]OperatorSet

func EmptyAPIMultiOwnerSet() APIMultiOwnerSet {
	return map[opregistry.APIKey]OperatorSet{}
}

func (s APIMultiOwnerSet) PopAPIKey() *opregistry.APIKey {
	for a := range s {
		api := &opregistry.APIKey{
			Group:   a.Group,
			Version: a.Version,
			Kind:    a.Kind,
			Plural:  a.Plural,
		}
		delete(s, a)
		return api
	}
	return nil
}

func (s APIMultiOwnerSet) PopAPIRequirers() OperatorSet {
	requirers := EmptyOperatorSet()
	for a := range s {
		for key, op := range s[a] {
			requirers[key] = op
		}
		delete(s, a)
		return requirers
	}
	return nil
}

type OperatorSourceInfo struct {
	Package        string
	Channel        string
	StartingCSV    string
	Catalog        registry.CatalogKey
	DefaultChannel bool
	Subscription   *v1alpha1.Subscription
}

func (i *OperatorSourceInfo) String() string {
	return fmt.Sprintf("%s/%s in %s/%s", i.Package, i.Channel, i.Catalog.Name, i.Catalog.Namespace)
}

var NoCatalog = registry.CatalogKey{Name: "", Namespace: ""}
var ExistingOperator = OperatorSourceInfo{Package: "", Channel: "", StartingCSV: "", Catalog: NoCatalog, DefaultChannel: false}

// OperatorSurface describes the API surfaces provided and required by an Operator.
type OperatorSurface interface {
	GetProvidedAPIs() APISet
	GetRequiredAPIs() APISet
	Identifier() string
	GetReplaces() string
	GetVersion() *semver.Version
	GetSourceInfo() *OperatorSourceInfo
	GetBundle() *api.Bundle
	Inline() bool
	GetProperties() []*api.Property
	GetSkips() []string
}

type Operator struct {
	Name         string
	Replaces     string
	ProvidedAPIs APISet
	RequiredAPIs APISet
	Version      *semver.Version
	Bundle       *api.Bundle
	SourceInfo   *OperatorSourceInfo
	Properties   []*api.Property
	Skips        []string
}

var _ OperatorSurface = &Operator{}

func NewOperatorFromBundle(bundle *api.Bundle, startingCSV string, sourceKey registry.CatalogKey, defaultChannel string) (*Operator, error) {
	parsedVersion, err := semver.ParseTolerant(bundle.Version)
	version := &parsedVersion
	if err != nil {
		version = nil
	}
	provided := APISet{}
	for _, gvk := range bundle.ProvidedApis {
		provided[opregistry.APIKey{Plural: gvk.Plural, Group: gvk.Group, Kind: gvk.Kind, Version: gvk.Version}] = struct{}{}
	}
	required := APISet{}
	for _, gvk := range bundle.RequiredApis {
		required[opregistry.APIKey{Plural: gvk.Plural, Group: gvk.Group, Kind: gvk.Kind, Version: gvk.Version}] = struct{}{}
	}
	sourceInfo := &OperatorSourceInfo{
		Package:     bundle.PackageName,
		Channel:     bundle.ChannelName,
		StartingCSV: startingCSV,
		Catalog:     sourceKey,
	}
	sourceInfo.DefaultChannel = sourceInfo.Channel == defaultChannel

	// legacy support - if the api doesn't contain properties/dependencies, build them from required/provided apis
	properties := bundle.Properties
	if len(properties) == 0 {
		properties, err = providedAPIsToProperties(provided)
		if err != nil {
			return nil, err
		}
	}
	if len(bundle.Dependencies) > 0 {
		ps, err := LegacyDependenciesToProperties(bundle.Dependencies)
		if err != nil {
			return nil, fmt.Errorf("failed to translate legacy dependencies to properties: %w", err)
		}
		properties = append(properties, ps...)
	} else {
		ps, err := RequiredAPIsToProperties(required)
		if err != nil {
			return nil, err
		}
		properties = append(properties, ps...)
	}

	o := &Operator{
		Name:         bundle.CsvName,
		Replaces:     bundle.Replaces,
		Version:      version,
		ProvidedAPIs: provided,
		RequiredAPIs: required,
		Bundle:       bundle,
		SourceInfo:   sourceInfo,
		Properties:   properties,
		Skips:        bundle.Skips,
	}

	if !o.Inline() {
		// TODO: Extract any necessary information from the Bundle
		// up-front and remove the bundle field altogether. For now,
		// take the easy opportunity to save heap space by discarding
		// two of the worst offenders.
		bundle.CsvJson = ""
		bundle.Object = nil
	}

	return o, nil
}

func NewOperatorFromV1Alpha1CSV(csv *v1alpha1.ClusterServiceVersion) (*Operator, error) {
	providedAPIs := EmptyAPISet()
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

	requiredAPIs := EmptyAPISet()
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
	dependencies, err := RequiredAPIsToProperties(requiredAPIs)
	if err != nil {
		return nil, err
	}
	properties = append(properties, dependencies...)

	return &Operator{
		Name:         csv.GetName(),
		Version:      &csv.Spec.Version.Version,
		ProvidedAPIs: providedAPIs,
		RequiredAPIs: requiredAPIs,
		SourceInfo:   &ExistingOperator,
		Properties:   properties,
	}, nil
}

func (o *Operator) GetProvidedAPIs() APISet {
	return o.ProvidedAPIs
}

func (o *Operator) GetRequiredAPIs() APISet {
	return o.RequiredAPIs
}

func (o *Operator) Identifier() string {
	return o.Name
}

func (o *Operator) GetReplaces() string {
	return o.Replaces
}

func (o *Operator) GetSkips() []string {
	return o.Skips
}

func (o *Operator) Package() string {
	if o.Bundle != nil {
		return o.Bundle.PackageName
	}
	return ""
}

func (o *Operator) Channel() string {
	if o.Bundle != nil {
		return o.Bundle.ChannelName
	}
	return ""
}

func (o *Operator) GetSourceInfo() *OperatorSourceInfo {
	return o.SourceInfo
}

func (o *Operator) GetBundle() *api.Bundle {
	return o.Bundle
}

func (o *Operator) GetVersion() *semver.Version {
	return o.Version
}

func (o *Operator) SemverRange() (semver.Range, error) {
	return semver.ParseRange(o.Bundle.SkipRange)
}

func (o *Operator) Inline() bool {
	return o.Bundle != nil && o.Bundle.GetBundlePath() == ""
}

func (o *Operator) GetProperties() []*api.Property {
	return o.Properties
}

func (o *Operator) DependencyPredicates() (predicates []OperatorPredicate, err error) {
	predicates = make([]OperatorPredicate, 0)
	for _, property := range o.Properties {
		predicate, err := PredicateForProperty(property)
		if err != nil {
			return nil, err
		}
		if predicate == nil {
			continue
		}
		predicates = append(predicates, predicate)
	}
	return
}

func RequiredAPIsToProperties(apis APISet) (out []*api.Property, err error) {
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

func providedAPIsToProperties(apis APISet) (out []*api.Property, err error) {
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

func LegacyDependenciesToProperties(dependencies []*api.Dependency) ([]*api.Property, error) {
	var result []*api.Property
	for _, dependency := range dependencies {
		switch dependency.Type {
		case "olm.gvk":
			type gvk struct {
				Group   string `json:"group"`
				Version string `json:"version"`
				Kind    string `json:"kind"`
			}
			var vfrom gvk
			if err := json.Unmarshal([]byte(dependency.Value), &vfrom); err != nil {
				return nil, fmt.Errorf("failed to unmarshal legacy 'olm.gvk' dependency: %w", err)
			}
			vto := gvk{
				Group:   vfrom.Group,
				Version: vfrom.Version,
				Kind:    vfrom.Kind,
			}
			vb, err := json.Marshal(&vto)
			if err != nil {
				return nil, fmt.Errorf("unexpected error marshaling generated 'olm.package.required' property: %w", err)
			}
			result = append(result, &api.Property{
				Type:  "olm.gvk.required",
				Value: string(vb),
			})
		case "olm.package":
			var vfrom struct {
				PackageName  string `json:"packageName"`
				VersionRange string `json:"version"`
			}
			if err := json.Unmarshal([]byte(dependency.Value), &vfrom); err != nil {
				return nil, fmt.Errorf("failed to unmarshal legacy 'olm.package' dependency: %w", err)
			}
			vto := struct {
				PackageName  string `json:"packageName"`
				VersionRange string `json:"versionRange"`
			}{
				PackageName:  vfrom.PackageName,
				VersionRange: vfrom.VersionRange,
			}
			vb, err := json.Marshal(&vto)
			if err != nil {
				return nil, fmt.Errorf("unexpected error marshaling generated 'olm.package.required' property: %w", err)
			}
			result = append(result, &api.Property{
				Type:  "olm.package.required",
				Value: string(vb),
			})
		case "olm.label":
			result = append(result, &api.Property{
				Type:  "olm.label.required",
				Value: dependency.Value,
			})
		}
	}
	return result, nil
}

func APISetToProperties(crds, apis APISet, deprecated bool) (out []*api.Property) {
	out = make([]*api.Property, 0)
	for a := range crds {
		val, err := json.Marshal(opregistry.GVKProperty{
			Group:   a.Group,
			Kind:    a.Kind,
			Version: a.Version,
		})
		if err != nil {
			panic(err)
		}
		out = append(out, &api.Property{
			Type:  opregistry.GVKType,
			Value: string(val),
		})
	}
	for a := range apis {
		val, err := json.Marshal(opregistry.GVKProperty{
			Group:   a.Group,
			Kind:    a.Kind,
			Version: a.Version,
		})
		if err != nil {
			panic(err)
		}
		out = append(out, &api.Property{
			Type:  opregistry.GVKType,
			Value: string(val),
		})
	}
	if deprecated {
		val, err := json.Marshal(opregistry.DeprecatedProperty{})
		if err != nil {
			panic(err)
		}
		out = append(out, &api.Property{
			Type:  opregistry.DeprecatedType,
			Value: string(val),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return
}

func APISetToDependencies(crds, apis APISet) (out []*api.Dependency) {
	if len(crds)+len(apis) == 0 {
		return nil
	}
	out = make([]*api.Dependency, 0)
	for a := range crds {
		val, err := json.Marshal(opregistry.GVKDependency{
			Group:   a.Group,
			Kind:    a.Kind,
			Version: a.Version,
		})
		if err != nil {
			panic(err)
		}
		out = append(out, &api.Dependency{
			Type:  opregistry.GVKType,
			Value: string(val),
		})
	}
	for a := range apis {
		val, err := json.Marshal(opregistry.GVKDependency{
			Group:   a.Group,
			Kind:    a.Kind,
			Version: a.Version,
		})
		if err != nil {
			panic(err)
		}
		out = append(out, &api.Dependency{
			Type:  opregistry.GVKType,
			Value: string(val),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return
}
