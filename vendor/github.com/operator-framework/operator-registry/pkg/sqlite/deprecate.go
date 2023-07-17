package sqlite

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

type SQLDeprecator interface {
	Deprecate() error
}

// BundleDeprecator removes bundles from the database
type BundleDeprecator struct {
	store   registry.Load
	bundles []string
}

// PackageDeprecator removes bundles and optionally entire packages from the index
type PackageDeprecator struct {
	*BundleDeprecator
	querier *SQLQuerier
}

var _ SQLDeprecator = &BundleDeprecator{}
var _ SQLDeprecator = &PackageDeprecator{}

func NewSQLDeprecatorForBundles(store registry.Load, bundles []string) *BundleDeprecator {
	return &BundleDeprecator{
		store:   store,
		bundles: bundles,
	}
}

func NewSQLDeprecatorForBundlesAndPackages(deprecator *BundleDeprecator, querier *SQLQuerier) *PackageDeprecator {
	return &PackageDeprecator{
		BundleDeprecator: deprecator,
		querier:          querier,
	}
}

func (d *BundleDeprecator) Deprecate() error {
	log := logrus.WithField("bundles", d.bundles)
	log.Info("deprecating bundles")

	var errs []error
	for _, bundlePath := range d.bundles {
		if err := d.store.DeprecateBundle(bundlePath); err != nil && !errors.Is(err, registry.ErrBundleImageNotInDatabase) {
			errs = append(errs, fmt.Errorf("error deprecating bundle %s: %s", bundlePath, err))
			if !errors.Is(err, registry.ErrRemovingDefaultChannelDuringDeprecation) {
				break
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}

// MaybeRemovePackages queries the DB to establish if any provided bundles are the head of the default channel of a package.
// If so, the list of bundles must also contain the head of all other channels in the package, otherwise an error is produced.
// If the heads of all channels are being deprecated (including the default channel), the package is removed entirely from the index.
// MaybeRemovePackages deletes all bundles from the associated package from the bundles array, so that the subsequent
// Deprecate() call can proceed with deprecating other potential bundles from other packages.
func (d *PackageDeprecator) MaybeRemovePackages() error {
	log := logrus.WithField("bundles", d.bundles)
	log.Info("allow-package-removal enabled: checking default channel heads for package removal")

	var errs []error
	var removedBundlePaths []string
	var remainingBundlePaths []string

	// Iterate over bundles list - see if any bundle is the head of a default channel in a package
	var packages []string
	for _, bundle := range d.bundles {
		found, err := d.querier.PackageFromDefaultChannelHeadBundle(context.TODO(), bundle)
		if err != nil {
			errs = append(errs, fmt.Errorf("error checking if bundle is default channel head %s: %s", bundle, err))
		}
		if found != "" {
			packages = append(packages, found)
		}
	}

	if len(packages) == 0 {
		log.Info("no head of default channel found - skipping package removal")
		return nil
	}

	// If so, ensure list contains head of all other channels in that package
	// If not, return error
	for _, pkg := range packages {
		channels, err := d.querier.ListChannels(context.TODO(), pkg)
		if err != nil {
			errs = append(errs, fmt.Errorf("error listing channels for package %s: %s", pkg, err))
		}
		for _, channel := range channels {
			found, err := d.querier.BundlePathForChannelHead(context.TODO(), pkg, channel)
			if err != nil {
				errs = append(errs, fmt.Errorf("error listing channel head for package %s: %s", pkg, err))
			}
			if !contains(found, d.bundles) {
				// terminal error
				errs = append(errs, fmt.Errorf("cannot deprecate default channel head from package without removing all other channel heads in package %s: must deprecate %s, head of channel %s", pkg, found, channel))
				return utilerrors.NewAggregate(errs)
			}
			removedBundlePaths = append(removedBundlePaths, found)
		}
	}

	// Remove associated package from index
	log.Infof("removing packages %#v", packages)
	for _, pkg := range packages {
		err := d.store.RemovePackage(pkg)
		if err != nil {
			errs = append(errs, fmt.Errorf("error removing package %s: %s", pkg, err))
		}
	}

	// Remove bundles from the removed package from the deprecation request
	// This enables other bundles to be deprecated via the expected flow
	// Build a new array with just the outstanding bundles
	for _, bundlePath := range d.bundles {
		if contains(bundlePath, removedBundlePaths) {
			continue
		}
		remainingBundlePaths = append(remainingBundlePaths, bundlePath)
	}
	d.bundles = remainingBundlePaths
	log.Infof("remaining bundles to deprecate %#v", d.bundles)

	return utilerrors.NewAggregate(errs)
}

func contains(bundlePath string, bundles []string) bool {
	for _, b := range bundles {
		if b == bundlePath {
			return true
		}
	}
	return false
}
