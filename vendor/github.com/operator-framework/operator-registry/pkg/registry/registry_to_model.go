package registry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/operator-framework/operator-registry/internal/model"
	"github.com/operator-framework/operator-registry/internal/property"
)

func ConvertRegistryBundleToModelBundles(b *Bundle) ([]model.Bundle, error) {
	var bundles []model.Bundle
	desc, err := b.csv.GetDescription()
	if err != nil {
		return nil, fmt.Errorf("Could not get description from bundle CSV:%s", err)
	}

	i, err := b.csv.GetIcons()
	if err != nil {
		return nil, fmt.Errorf("Could not get icon from bundle CSV:%s", err)
	}
	mIcon := &model.Icon{
		MediaType: "",
		Data:      []byte{},
	}
	if len(i) > 0 {
		mIcon.MediaType = i[0].MediaType
		mIcon.Data = []byte(i[0].Base64data)
	}

	pkg := &model.Package{
		Name:        b.Annotations.PackageName,
		Description: desc,
		Icon:        mIcon,
		Channels:    make(map[string]*model.Channel),
	}

	mb, err := registryBundleToModelBundle(b)
	mb.Package = pkg
	if err != nil {
		return nil, err
	}

	for _, ch := range extractChannels(b.Annotations.Channels) {
		newCh := &model.Channel{
			Name: ch,
		}
		chBundle := mb
		chBundle.Channel = newCh
		bundles = append(bundles, *chBundle)
	}
	return bundles, nil
}

func registryBundleToModelBundle(b *Bundle) (*model.Bundle, error) {
	bundleProps, err := PropertiesFromBundle(b)
	if err != nil {
		return nil, fmt.Errorf("error converting properties for internal model: %v", err)
	}

	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return nil, fmt.Errorf("Could not get CVS for bundle: %s", err)
	}
	replaces, err := csv.GetReplaces()
	if err != nil {
		return nil, fmt.Errorf("Could not get Replaces from CSV for bundle: %s", err)
	}
	skips, err := csv.GetSkips()
	if err != nil {
		return nil, fmt.Errorf("Could not get Skips from CSV for bundle: %s", err)
	}
	relatedImages, err := convertToModelRelatedImages(csv)
	if err != nil {
		return nil, fmt.Errorf("Could not get Related images from bundle: %v", err)
	}

	return &model.Bundle{
		Name:          csv.Name,
		Image:         b.BundleImage,
		Replaces:      replaces,
		Skips:         skips,
		Properties:    bundleProps,
		RelatedImages: relatedImages,
	}, nil
}

func PropertiesFromBundle(b *Bundle) ([]property.Property, error) {
	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return nil, fmt.Errorf("get csv: %v", err)
	}

	skips, err := csv.GetSkips()
	if err != nil {
		return nil, fmt.Errorf("get csv skips: %v", err)
	}

	var graphProps []property.Property
	replaces, err := csv.GetReplaces()
	if err != nil {
		return nil, fmt.Errorf("get csv replaces: %v", err)
	}
	for _, ch := range b.Channels {
		graphProps = append(graphProps, property.MustBuildChannel(ch, replaces))
	}

	for _, skip := range skips {
		graphProps = append(graphProps, property.MustBuildSkips(skip))
	}

	skipRange := csv.GetSkipRange()
	if skipRange != "" {
		graphProps = append(graphProps, property.MustBuildSkipRange(skipRange))
	}

	providedGVKs := map[property.GVK]struct{}{}
	requiredGVKs := map[property.GVKRequired]struct{}{}

	var packageProvidedProperty *property.Property
	var otherProps []property.Property

	for i, p := range b.Properties {
		switch p.Type {
		case property.TypeGVK:
			var v property.GVK
			if err := json.Unmarshal(p.Value, &v); err != nil {
				return nil, property.ParseError{Idx: i, Typ: p.Type, Err: err}
			}
			k := property.GVK{Group: v.Group, Kind: v.Kind, Version: v.Version}
			providedGVKs[k] = struct{}{}
		case property.TypePackage:
			var v property.Package
			if err := json.Unmarshal(p.Value, &v); err != nil {
				return nil, property.ParseError{Idx: i, Typ: p.Type, Err: err}
			}
			p := property.MustBuildPackage(v.PackageName, v.Version)
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
				return nil, property.ParseError{Idx: i, Typ: p.Type, Err: err}
			}
			k := property.GVKRequired{Group: v.Group, Kind: v.Kind, Version: v.Version}
			requiredGVKs[k] = struct{}{}
		case property.TypePackage:
			var v property.Package
			if err := json.Unmarshal(p.Value, &v); err != nil {
				return nil, property.ParseError{Idx: i, Typ: p.Type, Err: err}
			}
			packageRequiredProps = append(packageRequiredProps, property.MustBuildPackageRequired(v.PackageName, v.Version))
		}
	}

	version, err := b.Version()
	if err != nil {
		return nil, fmt.Errorf("get version: %v", err)
	}

	providedApis, err := b.ProvidedAPIs()
	if err != nil {
		return nil, fmt.Errorf("get provided apis: %v", err)
	}

	for p := range providedApis {
		k := property.GVK{Group: p.Group, Kind: p.Kind, Version: p.Version}
		if _, ok := providedGVKs[k]; !ok {
			providedGVKs[k] = struct{}{}
		}
	}
	requiredApis, err := b.RequiredAPIs()
	if err != nil {
		return nil, fmt.Errorf("get required apis: %v", err)
	}
	for p := range requiredApis {
		k := property.GVKRequired{Group: p.Group, Kind: p.Kind, Version: p.Version}
		if _, ok := requiredGVKs[k]; !ok {
			requiredGVKs[k] = struct{}{}
		}
	}

	var out []property.Property
	if packageProvidedProperty == nil {
		p := property.MustBuildPackage(b.Package, version)
		packageProvidedProperty = &p
	}
	out = append(out, *packageProvidedProperty)
	out = append(out, graphProps...)

	for p := range providedGVKs {
		out = append(out, property.MustBuildGVK(p.Group, p.Version, p.Kind))
	}

	for p := range requiredGVKs {
		out = append(out, property.MustBuildGVKRequired(p.Group, p.Version, p.Kind))
	}

	out = append(out, packageRequiredProps...)
	out = append(out, otherProps...)

	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return string(out[i].Value) < string(out[j].Value)
	})

	return out, nil
}

func convertToModelRelatedImages(csv *ClusterServiceVersion) ([]model.RelatedImage, error) {
	var objmap map[string]*json.RawMessage
	if err := json.Unmarshal(csv.Spec, &objmap); err != nil {
		return nil, err
	}

	rawValue, ok := objmap[relatedImages]
	if !ok || rawValue == nil {
		return nil, nil
	}

	type relatedImage struct {
		Name string `json:"name"`
		Ref  string `json:"image"`
	}
	var relatedImages []relatedImage
	if err := json.Unmarshal(*rawValue, &relatedImages); err != nil {
		return nil, err
	}
	mrelatedImages := []model.RelatedImage{}
	for _, img := range relatedImages {
		mrelatedImages = append(mrelatedImages, model.RelatedImage{Name: img.Name, Image: img.Ref})
	}
	return mrelatedImages, nil
}

func extractChannels(annotationChannels string) []string {
	var channels []string
	for _, ch := range strings.Split(annotationChannels, ",") {
		c := strings.TrimSpace(ch)
		if c != "" {
			channels = append(channels, ch)
		}
	}
	return channels
}
