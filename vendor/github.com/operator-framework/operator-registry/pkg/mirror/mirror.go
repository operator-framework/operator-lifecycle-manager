package mirror

import (
	"context"
	"fmt"
	"strings"

	"github.com/distribution/reference"
	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

type Mirrorer interface {
	Mirror() (map[string]string, error)
}

// DatabaseExtractor knows how to pull an index image and extract its database
type DatabaseExtractor interface {
	Extract(from string) (string, error)
}

type DatabaseExtractorFunc func(from string) (string, error)

func (f DatabaseExtractorFunc) Extract(from string) (string, error) {
	return f(from)
}

// ImageMirrorer knows how to mirror an image from one registry to another
type ImageMirrorer interface {
	Mirror(mapping map[string]string) error
}

type ImageMirrorerFunc func(mapping map[string]string) error

func (f ImageMirrorerFunc) Mirror(mapping map[string]string) error {
	return f(mapping)
}

type IndexImageMirrorer struct {
	ImageMirrorer     ImageMirrorer
	DatabaseExtractor DatabaseExtractor

	// options
	Source, Dest string
}

var _ Mirrorer = &IndexImageMirrorer{}

func NewIndexImageMirror(options ...ImageIndexMirrorOption) (*IndexImageMirrorer, error) {
	config := DefaultImageIndexMirrorerOptions()
	config.Apply(options)
	if err := config.Complete(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &IndexImageMirrorer{
		ImageMirrorer:     config.ImageMirrorer,
		DatabaseExtractor: config.DatabaseExtractor,
		Source:            config.Source,
		Dest:              config.Dest,
	}, nil
}

func (b *IndexImageMirrorer) Mirror() (map[string]string, error) {
	dbPath, err := b.DatabaseExtractor.Extract(b.Source)
	if err != nil {
		return nil, err
	}

	db, err := sqlite.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	migrator, err := sqlite.NewSQLLiteMigrator(db)
	if err != nil {
		return nil, err
	}
	if err := migrator.Migrate(context.TODO()); err != nil {
		return nil, err
	}

	querier := sqlite.NewSQLLiteQuerierFromDb(db)
	images, err := querier.ListImages(context.TODO())
	if err != nil {
		return nil, err
	}

	mapping := map[string]string{}

	var errs []error
	for _, img := range images {
		ref, err := reference.ParseNormalizedNamed(img)
		if err != nil {
			errs = append(errs, fmt.Errorf("couldn't parse image for mirroring (%s), skipping mirror: %s", img, err.Error()))
			continue
		}
		domain := reference.Domain(ref)
		mapping[ref.String()] = b.Dest + strings.TrimPrefix(ref.String(), domain)
	}

	if err := b.ImageMirrorer.Mirror(mapping); err != nil {
		errs = append(errs, fmt.Errorf("mirroring failed: %s", err.Error()))
	}

	return mapping, errors.NewAggregate(errs)
}
