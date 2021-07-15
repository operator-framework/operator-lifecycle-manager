package sqlite

import (
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

var _ SQLDeprecator = &BundleDeprecator{}

func NewSQLDeprecatorForBundles(store registry.Load, bundles []string) *BundleDeprecator {
	return &BundleDeprecator{
		store:   store,
		bundles: bundles,
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
