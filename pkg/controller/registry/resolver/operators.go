package resolver

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
	ProvidedAPIs() APISet
	RequiredAPIs() APISet
	Identifier() string
	Replaces() string
	Version() *semver.Version
	SourceInfo() *OperatorSourceInfo
	Bundle() *api.Bundle
	Inline() bool
	Properties() []*api.Property
	Skips() []string
}

type Operator struct {
	name         string
	replaces     string
	providedAPIs APISet
	requiredAPIs APISet
	version      *semver.Version
	bundle       *api.Bundle
	sourceInfo   *OperatorSourceInfo
	properties   []*api.Property
	skips        []string
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
		ps, err := legacyDependenciesToProperties(bundle.Dependencies)
		if err != nil {
			return nil, fmt.Errorf("failed to translate legacy dependencies to properties: %w", err)
		}
		properties = append(properties, ps...)
	} else {
		ps, err := requiredAPIsToProperties(required)
		if err != nil {
			return nil, err
		}
		properties = append(properties, ps...)
	}

	// legacy support - if the grpc api doesn't contain required/provided apis, fallback to csv parsing
	if len(required) == 0 && len(provided) == 0 && len(properties) == 0 {
		// fallback to csv parsing
		if bundle.CsvJson == "" {
			if bundle.GetBundlePath() != "" {
				return nil, fmt.Errorf("couldn't parse bundle path, missing provided and required apis")
			}

			return nil, fmt.Errorf("couldn't parse bundle, missing provided and required apis")
		}

		csv := &v1alpha1.ClusterServiceVersion{}
		if err := json.Unmarshal([]byte(bundle.CsvJson), csv); err != nil {
			return nil, err
		}

		op, err := NewOperatorFromV1Alpha1CSV(csv)
		if err != nil {
			return nil, err
		}
		op.sourceInfo = sourceInfo
		op.bundle = bundle
		return op, nil
	}

	return &Operator{
		name:         bundle.CsvName,
		replaces:     bundle.Replaces,
		version:      version,
		providedAPIs: provided,
		requiredAPIs: required,
		bundle:       bundle,
		sourceInfo:   sourceInfo,
		properties:   properties,
		skips:        bundle.Skips,
	}, nil
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
	dependencies, err := requiredAPIsToProperties(requiredAPIs)
	if err != nil {
		return nil, err
	}
	properties = append(properties, dependencies...)

	return &Operator{
		name:         csv.GetName(),
		version:      &csv.Spec.Version.Version,
		providedAPIs: providedAPIs,
		requiredAPIs: requiredAPIs,
		sourceInfo:   &ExistingOperator,
		properties:   properties,
	}, nil
}

func (o *Operator) ProvidedAPIs() APISet {
	return o.providedAPIs
}

func (o *Operator) RequiredAPIs() APISet {
	return o.requiredAPIs
}

func (o *Operator) Identifier() string {
	return o.name
}

func (o *Operator) Replaces() string {
	return o.replaces
}

func (o *Operator) Skips() []string {
	return o.skips
}

func (o *Operator) SetReplaces(replacing string) {
	o.replaces = replacing
}

func (o *Operator) Package() string {
	if o.bundle != nil {
		return o.bundle.PackageName
	}
	return ""
}

func (o *Operator) Channel() string {
	if o.bundle != nil {
		return o.bundle.ChannelName
	}
	return ""
}

func (o *Operator) SourceInfo() *OperatorSourceInfo {
	return o.sourceInfo
}

func (o *Operator) Bundle() *api.Bundle {
	return o.bundle
}

func (o *Operator) Version() *semver.Version {
	return o.version
}

func (o *Operator) Inline() bool {
	return o.bundle != nil && o.bundle.GetBundlePath() == ""
}

func (o *Operator) Properties() []*api.Property {
	return o.properties
}

func (o *Operator) DependencyPredicates() (predicates []OperatorPredicate, err error) {
	predicates = make([]OperatorPredicate, 0)
	for _, property := range o.Properties() {
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

func PredicateForProperty(property *api.Property) (OperatorPredicate, error) {
	if property == nil {
		return nil, nil
	}
	p, ok := predicates[property.Type]
	if !ok {
		return nil, nil
	}
	return p(property.Value)
}

var predicates = map[string]func(string) (OperatorPredicate, error){
	"olm.gvk.required":     predicateForRequiredGVKProperty,
	"olm.package.required": predicateForRequiredPackageProperty,
	"olm.label.required":   predicateForRequiredLabelProperty,
}

func predicateForRequiredGVKProperty(value string) (OperatorPredicate, error) {
	var gvk struct {
		Group   string `json:"group"`
		Version string `json:"version"`
		Kind    string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(value), &gvk); err != nil {
		return nil, err
	}
	return ProvidingAPI(opregistry.APIKey{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind,
	}), nil
}

func predicateForRequiredPackageProperty(value string) (OperatorPredicate, error) {
	var pkg struct {
		PackageName  string `json:"packageName"`
		VersionRange string `json:"versionRange"`
	}
	if err := json.Unmarshal([]byte(value), &pkg); err != nil {
		return nil, err
	}
	ver, err := semver.ParseRange(pkg.VersionRange)
	if err != nil {
		return nil, err
	}
	return And(WithPackage(pkg.PackageName), WithVersionInRange(ver)), nil
}

func predicateForRequiredLabelProperty(value string) (OperatorPredicate, error) {
	var label struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal([]byte(value), &label); err != nil {
		return nil, err
	}
	return WithLabel(label.Label), nil
}

func requiredAPIsToProperties(apis APISet) (out []*api.Property, err error) {
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

func legacyDependenciesToProperties(dependencies []*api.Dependency) ([]*api.Property, error) {
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
