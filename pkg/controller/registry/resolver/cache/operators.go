package cache

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

// todo: drop fields from cache.Entry and move to pkg/controller/operators/olm
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
		delete(difference, api)
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

type OperatorSourceInfo struct {
	Package        string
	Channel        string
	StartingCSV    string
	Catalog        SourceKey
	DefaultChannel bool
	Subscription   *v1alpha1.Subscription
}

func (i *OperatorSourceInfo) String() string {
	return fmt.Sprintf("%s/%s in %s/%s", i.Package, i.Channel, i.Catalog.Name, i.Catalog.Namespace)
}

type Entry struct {
	Name         string
	Replaces     string
	Skips        []string
	SkipRange    semver.Range
	ProvidedAPIs APISet
	RequiredAPIs APISet
	Version      *semver.Version
	SourceInfo   *OperatorSourceInfo
	Properties   []*api.Property
	BundlePath   string

	// Present exclusively to pipe inlined bundle
	// content. Resolver components shouldn't need to read this,
	// and it should eventually be possible to remove the field
	// altogether.
	Bundle *api.Bundle
}

func (o *Entry) Package() string {
	if si := o.SourceInfo; si != nil {
		return si.Package
	}
	return ""
}

func (o *Entry) Channel() string {
	if si := o.SourceInfo; si != nil {
		return si.Channel
	}
	return ""
}
