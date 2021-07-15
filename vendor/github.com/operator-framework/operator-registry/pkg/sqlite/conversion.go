package sqlite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/internal/model"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

func ToModel(ctx context.Context, q *SQLQuerier) (model.Model, error) {
	pkgs, err := initializeModelPackages(ctx, q)
	if err != nil {
		return nil, err
	}
	if err := populateModelChannels(ctx, pkgs, q); err != nil {
		return nil, fmt.Errorf("populate channels: %v", err)
	}
	if err := populatePackageIcons(ctx, pkgs, q); err != nil {
		return nil, fmt.Errorf("populate package icons: %v", err)
	}
	if err := pkgs.Validate(); err != nil {
		return nil, err
	}
	pkgs.Normalize()
	return pkgs, nil
}

func initializeModelPackages(ctx context.Context, q *SQLQuerier) (model.Model, error) {
	pkgNames, err := q.ListPackages(ctx)
	if err != nil {
		return nil, err
	}

	var rPkgs []registry.PackageManifest
	for _, pkgName := range pkgNames {
		rPkg, err := q.GetPackage(ctx, pkgName)
		if err != nil {
			return nil, err
		}
		rPkgs = append(rPkgs, *rPkg)
	}

	pkgs := model.Model{}
	for _, rPkg := range rPkgs {
		pkg := model.Package{
			Name: rPkg.PackageName,
		}

		pkg.Channels = map[string]*model.Channel{}
		for _, ch := range rPkg.Channels {
			channel := &model.Channel{
				Package: &pkg,
				Name:    ch.Name,
				Bundles: map[string]*model.Bundle{},
			}
			if ch.Name == rPkg.DefaultChannelName {
				pkg.DefaultChannel = channel
			}
			pkg.Channels[ch.Name] = channel
		}
		pkgs[pkg.Name] = &pkg
	}
	return pkgs, nil
}

func populateModelChannels(ctx context.Context, pkgs model.Model, q *SQLQuerier) error {
	bundles, err := q.ListBundles(ctx)
	if err != nil {
		return err
	}
	for _, bundle := range bundles {
		pkg, ok := pkgs[bundle.PackageName]
		if !ok {
			return fmt.Errorf("unknown package %q for bundle %q", bundle.PackageName, bundle.CsvName)
		}

		pkgChannel, ok := pkg.Channels[bundle.ChannelName]
		if !ok {
			return fmt.Errorf("unknown channel %q for bundle %q", bundle.ChannelName, bundle.CsvName)
		}

		mbundle, err := api.ConvertAPIBundleToModelBundle(bundle)
		if err != nil {
			return fmt.Errorf("convert bundle %q: %v", bundle.CsvName, err)
		}
		mbundle.Package = pkg
		mbundle.Channel = pkgChannel
		pkgChannel.Bundles[bundle.CsvName] = mbundle
	}
	return nil
}

// populatePackageIcons populates the package icons from the icon of bundle of the head
// of the default channel of each of the pacakges in pkgs.
func populatePackageIcons(ctx context.Context, pkgs model.Model, q *SQLQuerier) error {
	for _, pkg := range pkgs {
		head, err := q.GetBundleForChannel(ctx, pkg.Name, pkg.DefaultChannel.Name)
		if err != nil {
			return fmt.Errorf("get default channel head for package %q: %v", pkg.Name, err)
		}
		var csv v1alpha1.ClusterServiceVersion
		if err := json.Unmarshal([]byte(head.CsvJson), &csv); err != nil {
			return fmt.Errorf("unmarshal CSV json for bundle %q: %v", head.CsvName, err)
		}
		if len(csv.Spec.Icon) == 0 {
			continue
		}
		iconData, origErr := base64.StdEncoding.DecodeString(csv.Spec.Icon[0].Data)
		if origErr != nil {
			// Try decoding after removing spaces (this is a problem with the planetscale operator).
			iconData, err = base64.StdEncoding.DecodeString(strings.ReplaceAll(csv.Spec.Icon[0].Data, " ", ""))
			if err != nil {
				logrus.WithError(err).Warnf("base64 decode CSV icon for bundle %q", head.CsvName)
				continue
			}
		}
		if len(iconData) > 0 {
			pkg.Icon = &model.Icon{
				Data:      iconData,
				MediaType: csv.Spec.Icon[0].MediaType,
			}
		}
	}
	return nil
}
