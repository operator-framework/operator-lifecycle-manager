package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/lib/version"
	"github.com/operator-framework/api/pkg/operators"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-registry/alpha/model"
	"github.com/operator-framework/operator-registry/alpha/property"
)

func ConvertModelBundleToAPIBundle(b model.Bundle) (*Bundle, error) {
	props, err := parseProperties(b.Properties)
	if err != nil {
		return nil, fmt.Errorf("parse properties: %v", err)
	}

	csvJSON := b.CsvJSON
	if csvJSON == "" && len(props.CSVMetadatas) == 1 {
		var icons []v1alpha1.Icon
		if b.Package.Icon != nil {
			icons = []v1alpha1.Icon{{
				Data:      base64.StdEncoding.EncodeToString(b.Package.Icon.Data),
				MediaType: b.Package.Icon.MediaType,
			}}
		}
		csv := csvMetadataToCsv(props.CSVMetadatas[0])
		csv.Name = b.Name
		csv.Spec.Icon = icons
		csv.Spec.InstallStrategy = v1alpha1.NamedInstallStrategy{
			// This stub is required to avoid a panic in OLM's package server that results in
			// attemptint to write to a nil map.
			StrategyName: "deployment",
		}
		csv.Spec.Version = version.OperatorVersion{Version: b.Version}
		csv.Spec.RelatedImages = convertModelRelatedImagesToCSVRelatedImages(b.RelatedImages)
		if csv.Spec.Description == "" {
			csv.Spec.Description = b.Package.Description
		}
		csvData, err := json.Marshal(csv)
		if err != nil {
			return nil, err
		}
		csvJSON = string(csvData)
		if len(b.Objects) == 0 {
			b.Objects = []string{csvJSON}
		}
	}

	var deprecation *Deprecation
	if b.Deprecation != nil {
		deprecation = &Deprecation{
			Message: b.Deprecation.Message,
		}
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
		SkipRange:    b.SkipRange,
		Dependencies: apiDeps,
		Properties:   convertModelPropertiesToAPIProperties(b.Properties),
		Replaces:     b.Replaces,
		Skips:        b.Skips,
		CsvJson:      csvJSON,
		Object:       b.Objects,
		Deprecation:  deprecation,
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

	if len(props.CSVMetadatas) > 1 {
		return nil, fmt.Errorf("expected at most 1 property of type %q, found %d", property.TypeCSVMetadata, len(props.CSVMetadatas))
	}

	return props, nil
}

func csvMetadataToCsv(m property.CSVMetadata) v1alpha1.ClusterServiceVersion {
	return v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       operators.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: m.Annotations,
			Labels:      m.Labels,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			APIServiceDefinitions:     m.APIServiceDefinitions,
			CustomResourceDefinitions: m.CustomResourceDefinitions,
			Description:               m.Description,
			DisplayName:               m.DisplayName,
			InstallModes:              m.InstallModes,
			Keywords:                  m.Keywords,
			Links:                     m.Links,
			Maintainers:               m.Maintainers,
			Maturity:                  m.Maturity,
			MinKubeVersion:            m.MinKubeVersion,
			NativeAPIs:                m.NativeAPIs,
			Provider:                  m.Provider,
		},
	}
}

func gvksProvidedtoAPIGVKs(in []property.GVK) []*GroupVersionKind {
	// nolint:prealloc
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
	// nolint:prealloc
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
	// nolint:prealloc
	var out []*Property
	for _, prop := range props {
		// NOTE: This is a special case filter to prevent problems with existing client implementations that
		//       project bundle properties into CSV annotations and store those CSVs in a size-constrained
		//       storage backend (e.g. etcd via kube-apiserver). If the bundle object property has data inlined
		//       in its `Data` field, this CSV annotation projection would cause the size of the on-cluster
		//       CSV to at least double, which is untenable since CSVs already have known issues running up
		//       against etcd size constraints.
		if prop.Type == property.TypeBundleObject || prop.Type == property.TypeCSVMetadata {
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
	// nolint:prealloc
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

func convertModelRelatedImagesToCSVRelatedImages(in []model.RelatedImage) []v1alpha1.RelatedImage {
	// nolint:prealloc
	var out []v1alpha1.RelatedImage
	for _, ri := range in {
		out = append(out, v1alpha1.RelatedImage{
			Name:  ri.Name,
			Image: ri.Image,
		})
	}
	return out
}
