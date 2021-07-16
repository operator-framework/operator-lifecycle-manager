package api

import (
	"encoding/json"
	"fmt"

	"github.com/operator-framework/operator-registry/internal/model"
	"github.com/operator-framework/operator-registry/internal/property"
)

func ConvertModelBundleToAPIBundle(b model.Bundle) (*Bundle, error) {
	props, err := parseProperties(b.Properties)
	if err != nil {
		return nil, fmt.Errorf("parse properties: %v", err)
	}
	skipRange := ""
	if len(props.SkipRanges) > 0 {
		skipRange = string(props.SkipRanges[0])
	}

	apiDeps, err := convertModelPropertiesToAPIDependencies(b.Properties)
	if err != nil {
		return nil, fmt.Errorf("convert model properties to api dependencies: %v", err)
	}
	return &Bundle{
		CsvName:      b.Name,
		PackageName:  b.Package.Name,
		ChannelName:  b.Channel.Name,
		BundlePath:   b.Image,
		ProvidedApis: gvksProvidedtoAPIGVKs(props.GVKs),
		RequiredApis: gvksRequirestoAPIGVKs(props.GVKsRequired),
		Version:      props.Packages[0].Version,
		SkipRange:    skipRange,
		Dependencies: apiDeps,
		Properties:   convertModelPropertiesToAPIProperties(b.Properties),
		Replaces:     b.Replaces,
		Skips:        b.Skips,
		CsvJson:      b.CsvJSON,
		Object:       b.Objects,
	}, nil
}

func parseProperties(in []property.Property) (*property.Properties, error) {
	props, err := property.Parse(in)
	if err != nil {
		return nil, err
	}

	if len(props.Packages) != 1 {
		return nil, fmt.Errorf("expected exactly 1 property of type %q, found %d", property.TypePackage, len(props.Packages))
	}

	if len(props.SkipRanges) > 1 {
		return nil, fmt.Errorf("multiple properties of type %q not allowed", property.TypeSkipRange)
	}

	return props, nil
}

func gvksProvidedtoAPIGVKs(in []property.GVK) []*GroupVersionKind {
	var out []*GroupVersionKind
	for _, gvk := range in {
		out = append(out, &GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind,
		})
	}
	return out
}
func gvksRequirestoAPIGVKs(in []property.GVKRequired) []*GroupVersionKind {
	var out []*GroupVersionKind
	for _, gvk := range in {
		out = append(out, &GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind,
		})
	}
	return out
}

func convertModelPropertiesToAPIProperties(props []property.Property) []*Property {
	var out []*Property
	for _, prop := range props {

		// NOTE: This is a special case filter to prevent problems with existing client implementations that
		//       project bundle properties into CSV annotations and store those CSVs in a size-constrained
		//       storage backend (e.g. etcd via kube-apiserver). If the bundle object property has data inlined
		//       in its `Data` field, this CSV annotation projection would cause the size of the on-cluster
		//       CSV to at least double, which is untenable since CSVs already have known issues running up
		//       against etcd size constraints.
		if prop.Type == property.TypeBundleObject {
			continue
		}

		out = append(out, &Property{
			Type:  prop.Type,
			Value: string(prop.Value),
		})
	}
	return out
}

func convertModelPropertiesToAPIDependencies(props []property.Property) ([]*Dependency, error) {
	var out []*Dependency
	for _, prop := range props {
		switch prop.Type {
		case property.TypeGVKRequired:
			out = append(out, &Dependency{
				Type:  property.TypeGVK,
				Value: string(prop.Value),
			})
		case property.TypePackageRequired:
			var v property.PackageRequired
			if err := json.Unmarshal(prop.Value, &v); err != nil {
				return nil, err
			}
			pkg := property.MustBuildPackage(v.PackageName, v.VersionRange)
			out = append(out, &Dependency{
				Type:  pkg.Type,
				Value: string(pkg.Value),
			})
		}
	}
	return out, nil
}
