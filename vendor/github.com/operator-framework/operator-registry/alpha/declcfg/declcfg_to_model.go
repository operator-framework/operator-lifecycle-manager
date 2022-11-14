package declcfg

import (
	"fmt"

	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/operator-framework/operator-registry/alpha/model"
	"github.com/operator-framework/operator-registry/alpha/property"
)

func ConvertToModel(cfg DeclarativeConfig) (model.Model, error) {
	mpkgs := model.Model{}
	defaultChannels := map[string]string{}
	for _, p := range cfg.Packages {
		if p.Name == "" {
			return nil, fmt.Errorf("config contains package with no name")
		}

		if _, ok := mpkgs[p.Name]; ok {
			return nil, fmt.Errorf("duplicate package %q", p.Name)
		}

		mpkg := &model.Package{
			Name:        p.Name,
			Description: p.Description,
			Channels:    map[string]*model.Channel{},
		}
		if p.Icon != nil {
			mpkg.Icon = &model.Icon{
				Data:      p.Icon.Data,
				MediaType: p.Icon.MediaType,
			}
		}
		defaultChannels[p.Name] = p.DefaultChannel
		mpkgs[p.Name] = mpkg
	}

	channelDefinedEntries := map[string]sets.String{}
	for _, c := range cfg.Channels {
		mpkg, ok := mpkgs[c.Package]
		if !ok {
			return nil, fmt.Errorf("unknown package %q for channel %q", c.Package, c.Name)
		}

		if c.Name == "" {
			return nil, fmt.Errorf("package %q contains channel with no name", c.Package)
		}

		if _, ok := mpkg.Channels[c.Name]; ok {
			return nil, fmt.Errorf("package %q has duplicate channel %q", c.Package, c.Name)
		}

		mch := &model.Channel{
			Package: mpkg,
			Name:    c.Name,
			Bundles: map[string]*model.Bundle{},
			// NOTICE: The field Properties of the type Channel is for internal use only.
			//   DO NOT use it for any public-facing functionalities.
			//   This API is in alpha stage and it is subject to change.
			Properties: c.Properties,
		}

		cde := sets.NewString()
		for _, entry := range c.Entries {
			if _, ok := mch.Bundles[entry.Name]; ok {
				return nil, fmt.Errorf("invalid package %q, channel %q: duplicate entry %q", c.Package, c.Name, entry.Name)
			}
			cde = cde.Insert(entry.Name)
			mch.Bundles[entry.Name] = &model.Bundle{
				Package:   mpkg,
				Channel:   mch,
				Name:      entry.Name,
				Replaces:  entry.Replaces,
				Skips:     entry.Skips,
				SkipRange: entry.SkipRange,
			}
		}
		channelDefinedEntries[c.Package] = cde

		mpkg.Channels[c.Name] = mch

		defaultChannelName := defaultChannels[c.Package]
		if defaultChannelName == c.Name {
			mpkg.DefaultChannel = mch
		}
	}

	// packageBundles tracks the set of bundle names for each package
	// and is used to detect duplicate bundles.
	packageBundles := map[string]sets.String{}

	for _, b := range cfg.Bundles {
		if b.Package == "" {
			return nil, fmt.Errorf("package name must be set for bundle %q", b.Name)
		}
		mpkg, ok := mpkgs[b.Package]
		if !ok {
			return nil, fmt.Errorf("unknown package %q for bundle %q", b.Package, b.Name)
		}

		bundles, ok := packageBundles[b.Package]
		if !ok {
			bundles = sets.NewString()
		}
		if bundles.Has(b.Name) {
			return nil, fmt.Errorf("package %q has duplicate bundle %q", b.Package, b.Name)
		}
		bundles.Insert(b.Name)
		packageBundles[b.Package] = bundles

		props, err := property.Parse(b.Properties)
		if err != nil {
			return nil, fmt.Errorf("parse properties for bundle %q: %v", b.Name, err)
		}

		if len(props.Packages) != 1 {
			return nil, fmt.Errorf("package %q bundle %q must have exactly 1 %q property, found %d", b.Package, b.Name, property.TypePackage, len(props.Packages))
		}

		if b.Package != props.Packages[0].PackageName {
			return nil, fmt.Errorf("package %q does not match %q property %q", b.Package, property.TypePackage, props.Packages[0].PackageName)
		}

		// Parse version from the package property.
		rawVersion := props.Packages[0].Version
		ver, err := semver.Parse(rawVersion)
		if err != nil {
			return nil, fmt.Errorf("error parsing bundle %q version %q: %v", b.Name, rawVersion, err)
		}

		channelDefinedEntries[b.Package] = channelDefinedEntries[b.Package].Delete(b.Name)
		found := false
		for _, mch := range mpkg.Channels {
			if mb, ok := mch.Bundles[b.Name]; ok {
				found = true
				mb.Image = b.Image
				mb.Properties = b.Properties
				mb.RelatedImages = relatedImagesToModelRelatedImages(b.RelatedImages)
				mb.CsvJSON = b.CsvJSON
				mb.Objects = b.Objects
				mb.PropertiesP = props
				mb.Version = ver
			}
		}
		if !found {
			return nil, fmt.Errorf("package %q, bundle %q not found in any channel entries", b.Package, b.Name)
		}
	}

	for pkg, entries := range channelDefinedEntries {
		if entries.Len() > 0 {
			return nil, fmt.Errorf("no olm.bundle blobs found in package %q for olm.channel entries %s", pkg, entries.List())
		}
	}

	for _, mpkg := range mpkgs {
		defaultChannelName := defaultChannels[mpkg.Name]
		if defaultChannelName != "" && mpkg.DefaultChannel == nil {
			dch := &model.Channel{
				Package: mpkg,
				Name:    defaultChannelName,
				Bundles: map[string]*model.Bundle{},
			}
			mpkg.DefaultChannel = dch
			mpkg.Channels[dch.Name] = dch
		}
	}

	if err := mpkgs.Validate(); err != nil {
		return nil, err
	}
	mpkgs.Normalize()
	return mpkgs, nil
}

func relatedImagesToModelRelatedImages(in []RelatedImage) []model.RelatedImage {
	var out []model.RelatedImage
	for _, p := range in {
		out = append(out, model.RelatedImage{
			Name:  p.Name,
			Image: p.Image,
		})
	}
	return out
}
