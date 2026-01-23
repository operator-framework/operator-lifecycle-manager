package registry

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/operator-framework/operator-registry/alpha/property"
)

func ObjectsAndPropertiesFromBundle(b *Bundle) ([]string, []property.Property, error) {
	providedGVKs := map[property.GVK]struct{}{}
	requiredGVKs := map[property.GVKRequired]struct{}{}

	var packageProvidedProperty *property.Property
	var otherProps []property.Property

	for i, p := range b.Properties {
		switch p.Type {
		case property.TypeGVK:
			var v property.GVK
			if err := json.Unmarshal(p.Value, &v); err != nil {
				return nil, nil, property.ParseError{Idx: i, Typ: p.Type, Err: err}
			}
			k := property.GVK{Group: v.Group, Kind: v.Kind, Version: v.Version}
			providedGVKs[k] = struct{}{}
		case property.TypePackage:
			var v property.Package
			if err := json.Unmarshal(p.Value, &v); err != nil {
				return nil, nil, property.ParseError{Idx: i, Typ: p.Type, Err: err}
			}
			p := property.MustBuildPackageRelease(v.PackageName, v.Version, v.Release)
			packageProvidedProperty = &p
		default:
			otherProps = append(otherProps, property.Property{
				Type:  p.Type,
				Value: p.Value,
			})
		}
	}

	var packageRequiredProps []property.Property
	for i, p := range b.Dependencies {
		switch p.Type {
		case property.TypeGVK:
			var v property.GVK
			if err := json.Unmarshal(p.Value, &v); err != nil {
				return nil, nil, property.ParseError{Idx: i, Typ: p.Type, Err: err}
			}
			k := property.GVKRequired(v)
			requiredGVKs[k] = struct{}{}
		case property.TypePackage:
			var v property.Package
			if err := json.Unmarshal(p.Value, &v); err != nil {
				return nil, nil, property.ParseError{Idx: i, Typ: p.Type, Err: err}
			}
			packageRequiredProps = append(packageRequiredProps, property.MustBuildPackageRequired(v.PackageName, v.Version))
		default:
			otherProps = append(otherProps, property.Property{
				Type:  p.Type,
				Value: p.Value,
			})
		}
	}

	version, err := b.Version()
	if err != nil {
		return nil, nil, fmt.Errorf("get version: %v", err)
	}

	release, err := b.Release()
	if err != nil {
		return nil, nil, fmt.Errorf("get release: %v", err)
	}

	providedApis, err := b.ProvidedAPIs()
	if err != nil {
		return nil, nil, fmt.Errorf("get provided apis: %v", err)
	}

	for p := range providedApis {
		k := property.GVK{Group: p.Group, Kind: p.Kind, Version: p.Version}
		if _, ok := providedGVKs[k]; !ok {
			providedGVKs[k] = struct{}{}
		}
	}
	requiredApis, err := b.RequiredAPIs()
	if err != nil {
		return nil, nil, fmt.Errorf("get required apis: %v", err)
	}
	for p := range requiredApis {
		k := property.GVKRequired{Group: p.Group, Kind: p.Kind, Version: p.Version}
		if _, ok := requiredGVKs[k]; !ok {
			requiredGVKs[k] = struct{}{}
		}
	}

	// nolint:prealloc
	var (
		props   []property.Property
		objects []string
	)
	for _, obj := range b.Objects {
		objData, err := json.Marshal(obj)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal object %s/%s (%s) to json: %v", obj.GetName(), obj.GetNamespace(), obj.GroupVersionKind(), err)
		}
		props = append(props, property.MustBuildBundleObject(objData))
		objects = append(objects, string(objData))
	}

	if packageProvidedProperty == nil {
		p := property.MustBuildPackageRelease(b.Package, version, release)
		packageProvidedProperty = &p
	}
	props = append(props, *packageProvidedProperty)

	for p := range providedGVKs {
		props = append(props, property.MustBuildGVK(p.Group, p.Version, p.Kind))
	}

	for p := range requiredGVKs {
		props = append(props, property.MustBuildGVKRequired(p.Group, p.Version, p.Kind))
	}

	props = append(props, packageRequiredProps...)
	props = append(props, otherProps...)

	sort.Slice(props, func(i, j int) bool {
		if props[i].Type != props[j].Type {
			return props[i].Type < props[j].Type
		}
		return string(props[i].Value) < string(props[j].Value)
	})

	return objects, props, nil
}
