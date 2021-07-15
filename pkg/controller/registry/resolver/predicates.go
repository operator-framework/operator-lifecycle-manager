package resolver

import (
	"encoding/json"

	"github.com/blang/semver/v4"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

type OperatorPredicate interface {
	Test(*Operator) bool
}

type OperatorPredicateFunc func(*Operator) bool

func (opf OperatorPredicateFunc) Test(o *Operator) bool {
	return opf(o)
}

func WithCSVName(name string) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		return o.name == name
	})
}

func WithChannel(channel string) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		// all operators match the empty channel
		if channel == "" {
			return true
		}
		if o.bundle == nil {
			return false
		}
		return o.bundle.ChannelName == channel
	})
}

func WithPackage(pkg string) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		for _, p := range o.Properties() {
			if p.Type != opregistry.PackageType {
				continue
			}
			var prop opregistry.PackageProperty
			err := json.Unmarshal([]byte(p.Value), &prop)
			if err != nil {
				continue
			}
			if prop.PackageName == pkg {
				return true
			}
		}
		return o.Package() == pkg
	})
}

func WithVersionInRange(r semver.Range) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		for _, p := range o.Properties() {
			if p.Type != opregistry.PackageType {
				continue
			}
			var prop opregistry.PackageProperty
			err := json.Unmarshal([]byte(p.Value), &prop)
			if err != nil {
				continue
			}
			ver, err := semver.Parse(prop.Version)
			if err != nil {
				continue
			}
			if r(ver) {
				return true
			}
		}
		return o.version != nil && r(*o.version)
	})
}

func WithLabel(label string) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		for _, p := range o.Properties() {
			if p.Type != opregistry.LabelType {
				continue
			}
			var prop opregistry.LabelProperty
			err := json.Unmarshal([]byte(p.Value), &prop)
			if err != nil {
				continue
			}
			if prop.Label == label {
				return true
			}
		}
		return false
	})
}

func WithCatalog(key registry.CatalogKey) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		return key.Equal(o.SourceInfo().Catalog)
	})
}

func ProvidingAPI(api opregistry.APIKey) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		for _, p := range o.Properties() {
			if p.Type != opregistry.GVKType {
				continue
			}
			var prop opregistry.GVKProperty
			err := json.Unmarshal([]byte(p.Value), &prop)
			if err != nil {
				continue
			}
			if prop.Kind == api.Kind && prop.Version == api.Version && prop.Group == api.Group {
				return true
			}
		}
		return false
	})
}

func SkipRangeIncludes(version semver.Version) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		// TODO: lift range parsing to OperatorSurface
		semverRange, err := semver.ParseRange(o.bundle.SkipRange)
		return err == nil && semverRange(version)
	})
}

func Replaces(name string) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		if o.Replaces() == name {
			return true
		}
		for _, s := range o.bundle.Skips {
			if s == name {
				return true
			}
		}
		return false
	})
}

func And(p ...OperatorPredicate) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		for _, l := range p {
			if l.Test(o) == false {
				return false
			}
		}
		return true
	})
}

func Or(p ...OperatorPredicate) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		for _, l := range p {
			if l.Test(o) == true {
				return true
			}
		}
		return false
	})
}

func True() OperatorPredicate {
	return OperatorPredicateFunc(func(*Operator) bool {
		return true
	})
}

func False() OperatorPredicate {
	return OperatorPredicateFunc(func(*Operator) bool {
		return false
	})
}

func CountingPredicate(p OperatorPredicate, n *int) OperatorPredicate {
	return OperatorPredicateFunc(func(o *Operator) bool {
		if p.Test(o) {
			*n++
			return true
		}
		return false
	})
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
