package internal

import (
	"fmt"

	"github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

var PackageManifestValidator interfaces.Validator = interfaces.ValidatorFunc(validatePackageManifests)

func validatePackageManifests(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.PackageManifest:
			results = append(results, validatePackageManifest(v))
		}
	}
	return results
}

func validatePackageManifest(pkg *manifests.PackageManifest) errors.ManifestResult {
	result := errors.ManifestResult{Name: pkg.PackageName}
	result.Add(validateChannels(pkg)...)
	return result
}

func validateChannels(pkg *manifests.PackageManifest) (errs []errors.Error) {
	if pkg.PackageName == "" {
		errs = append(errs, errors.ErrInvalidPackageManifest("packageName empty", pkg.PackageName))
	}
	numChannels := len(pkg.Channels)
	if numChannels == 0 {
		errs = append(errs, errors.ErrInvalidPackageManifest("channels empty", pkg.PackageName))
		return errs
	}
	if pkg.DefaultChannelName == "" && numChannels > 1 {
		errs = append(errs, errors.ErrInvalidPackageManifest("default channel is empty but more than one channel exists", pkg.PackageName))
	}

	seen := map[string]struct{}{}
	for i, c := range pkg.Channels {
		if c.Name == "" {
			errs = append(errs, errors.ErrInvalidPackageManifest(fmt.Sprintf("channel %d name is empty", i), pkg.PackageName))
		}
		if c.CurrentCSVName == "" {
			errs = append(errs, errors.ErrInvalidPackageManifest(fmt.Sprintf("channel %q currentCSV is empty", c.Name), pkg.PackageName))
		}
		if _, ok := seen[c.Name]; ok {
			errs = append(errs, errors.ErrInvalidPackageManifest(fmt.Sprintf("duplicate package manifest channel name %q", c.Name), pkg.PackageName))
		}
		seen[c.Name] = struct{}{}
	}
	if _, found := seen[pkg.DefaultChannelName]; pkg.DefaultChannelName != "" && !found {
		errs = append(errs, errors.ErrInvalidPackageManifest(fmt.Sprintf("default channel %q not found in the list of declared channels", pkg.DefaultChannelName), pkg.PackageName))
	}

	return errs
}
