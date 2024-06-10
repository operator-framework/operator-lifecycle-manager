package action

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"

	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/alpha/property"
	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"github.com/operator-framework/operator-registry/pkg/lib/log"
	"github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

var logDeprecationMessage sync.Once

type RefType uint

const (
	RefBundleImage RefType = 1 << iota
	RefSqliteImage
	RefSqliteFile
	RefDCImage
	RefDCDir
	RefBundleDir

	RefAll = 0
)

func (r RefType) Allowed(refType RefType) bool {
	return r == RefAll || r&refType == refType
}

var ErrNotAllowed = errors.New("not allowed")

type Render struct {
	Refs             []string
	Registry         image.Registry
	AllowedRefMask   RefType
	Migrate          bool
	ImageRefTemplate *template.Template

	skipSqliteDeprecationLog bool
}

func (r Render) Run(ctx context.Context) (*declcfg.DeclarativeConfig, error) {
	if r.skipSqliteDeprecationLog {
		// exhaust once with a no-op function.
		logDeprecationMessage.Do(func() {})
	}
	if r.Registry == nil {
		reg, err := r.createRegistry()
		if err != nil {
			return nil, fmt.Errorf("create registry: %v", err)
		}
		defer reg.Destroy()
		r.Registry = reg
	}

	var cfgs []declcfg.DeclarativeConfig
	for _, ref := range r.Refs {
		cfg, err := r.renderReference(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("render reference %q: %w", ref, err)
		}
		moveBundleObjectsToEndOfPropertySlices(cfg)

		for _, b := range cfg.Bundles {
			sort.Slice(b.RelatedImages, func(i, j int) bool {
				return b.RelatedImages[i].Image < b.RelatedImages[j].Image
			})
		}

		if r.Migrate {
			if err := migrate(cfg); err != nil {
				return nil, fmt.Errorf("migrate: %v", err)
			}
		}

		cfgs = append(cfgs, *cfg)
	}

	return combineConfigs(cfgs), nil
}

func (r Render) createRegistry() (*containerdregistry.Registry, error) {
	cacheDir, err := os.MkdirTemp("", "render-registry-")
	if err != nil {
		return nil, fmt.Errorf("create tempdir: %v", err)
	}

	reg, err := containerdregistry.NewRegistry(
		containerdregistry.WithCacheDir(cacheDir),

		// The containerd registry impl is somewhat verbose, even on the happy path,
		// so discard all logger logs. Any important failures will be returned from
		// registry methods and eventually logged as fatal errors.
		containerdregistry.WithLog(log.Null()),
	)
	if err != nil {
		return nil, err
	}
	return reg, nil
}

func (r Render) renderReference(ctx context.Context, ref string) (*declcfg.DeclarativeConfig, error) {
	stat, err := os.Stat(ref)
	if err != nil {
		return r.imageToDeclcfg(ctx, ref)
	}
	if stat.IsDir() {
		dirEntries, err := os.ReadDir(ref)
		if err != nil {
			return nil, err
		}
		if isBundle(dirEntries) {
			// Looks like a bundle directory
			if !r.AllowedRefMask.Allowed(RefBundleDir) {
				return nil, fmt.Errorf("cannot render bundle directory %q: %w", ref, ErrNotAllowed)
			}
			return r.renderBundleDirectory(ref)
		}

		// Otherwise, assume it is a declarative config root directory.
		if !r.AllowedRefMask.Allowed(RefDCDir) {
			return nil, fmt.Errorf("cannot render declarative config directory: %w", ErrNotAllowed)
		}
		return declcfg.LoadFS(ctx, os.DirFS(ref))
	}
	// The only supported file type is an sqlite DB file,
	// since declarative configs will be in a directory.
	if err := checkDBFile(ref); err != nil {
		return nil, err
	}
	if !r.AllowedRefMask.Allowed(RefSqliteFile) {
		return nil, fmt.Errorf("cannot render sqlite file: %w", ErrNotAllowed)
	}

	db, err := sqlite.Open(ref)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return sqliteToDeclcfg(ctx, db)
}

func (r Render) imageToDeclcfg(ctx context.Context, imageRef string) (*declcfg.DeclarativeConfig, error) {
	ref := image.SimpleReference(imageRef)
	if err := r.Registry.Pull(ctx, ref); err != nil {
		return nil, err
	}
	labels, err := r.Registry.Labels(ctx, ref)
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "render-unpack-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	if err := r.Registry.Unpack(ctx, ref, tmpDir); err != nil {
		return nil, err
	}

	var cfg *declcfg.DeclarativeConfig
	if dbFile, ok := labels[containertools.DbLocationLabel]; ok {
		if !r.AllowedRefMask.Allowed(RefSqliteImage) {
			return nil, fmt.Errorf("cannot render sqlite image: %w", ErrNotAllowed)
		}
		db, err := sqlite.Open(filepath.Join(tmpDir, dbFile))
		if err != nil {
			return nil, err
		}
		defer db.Close()
		cfg, err = sqliteToDeclcfg(ctx, db)
		if err != nil {
			return nil, err
		}
	} else if configsDir, ok := labels[containertools.ConfigsLocationLabel]; ok {
		if !r.AllowedRefMask.Allowed(RefDCImage) {
			return nil, fmt.Errorf("cannot render declarative config image: %w", ErrNotAllowed)
		}
		cfg, err = declcfg.LoadFS(ctx, os.DirFS(filepath.Join(tmpDir, configsDir)))
		if err != nil {
			return nil, err
		}
	} else if _, ok := labels[bundle.PackageLabel]; ok {
		if !r.AllowedRefMask.Allowed(RefBundleImage) {
			return nil, fmt.Errorf("cannot render bundle image: %w", ErrNotAllowed)
		}
		img, err := registry.NewImageInput(ref, tmpDir)
		if err != nil {
			return nil, err
		}

		bundle, err := bundleToDeclcfg(img.Bundle)
		if err != nil {
			return nil, err
		}
		cfg = &declcfg.DeclarativeConfig{Bundles: []declcfg.Bundle{*bundle}}
	} else {
		labelKeys := sets.StringKeySet(labels)
		labelVals := []string{}
		for _, k := range labelKeys.List() {
			labelVals = append(labelVals, fmt.Sprintf("  %s=%s", k, labels[k]))
		}
		if len(labelVals) > 0 {
			return nil, fmt.Errorf("render %q: image type could not be determined, found labels\n%s", ref, strings.Join(labelVals, "\n"))
		} else {
			return nil, fmt.Errorf("render %q: image type could not be determined: image has no labels", ref)
		}
	}
	return cfg, nil
}

// checkDBFile returns an error if ref is not an sqlite3 database.
func checkDBFile(ref string) error {
	typ, err := filetype.MatchFile(ref)
	if err != nil {
		return err
	}
	if typ != matchers.TypeSqlite {
		return fmt.Errorf("ref %q has unsupported file type: %s", ref, typ)
	}
	return nil
}

func sqliteToDeclcfg(ctx context.Context, db *sql.DB) (*declcfg.DeclarativeConfig, error) {
	logDeprecationMessage.Do(func() {
		sqlite.LogSqliteDeprecation()
	})

	migrator, err := sqlite.NewSQLLiteMigrator(db)
	if err != nil {
		return nil, err
	}
	if migrator == nil {
		return nil, fmt.Errorf("failed to load migrator")
	}

	if err := migrator.Migrate(ctx); err != nil {
		return nil, err
	}

	q := sqlite.NewSQLLiteQuerierFromDb(db)
	m, err := sqlite.ToModel(ctx, q)
	if err != nil {
		return nil, err
	}

	cfg := declcfg.ConvertFromModel(m)

	if err := populateDBRelatedImages(ctx, &cfg, db); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func populateDBRelatedImages(ctx context.Context, cfg *declcfg.DeclarativeConfig, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT image, operatorbundle_name FROM related_image")
	if err != nil {
		return err
	}
	defer rows.Close()

	images := map[string]sets.String{}
	for rows.Next() {
		var (
			img        sql.NullString
			bundleName sql.NullString
		)
		if err := rows.Scan(&img, &bundleName); err != nil {
			return err
		}
		if !img.Valid || !bundleName.Valid {
			continue
		}
		m, ok := images[bundleName.String]
		if !ok {
			m = sets.NewString()
		}
		m.Insert(img.String)
		images[bundleName.String] = m
	}

	for i, b := range cfg.Bundles {
		ris, ok := images[b.Name]
		if !ok {
			continue
		}
		for _, ri := range b.RelatedImages {
			if ris.Has(ri.Image) {
				ris.Delete(ri.Image)
			}
		}
		for ri := range ris {
			cfg.Bundles[i].RelatedImages = append(cfg.Bundles[i].RelatedImages, declcfg.RelatedImage{Image: ri})
		}
	}
	return nil
}

func bundleToDeclcfg(bundle *registry.Bundle) (*declcfg.Bundle, error) {
	objs, props, err := registry.ObjectsAndPropertiesFromBundle(bundle)
	if err != nil {
		return nil, fmt.Errorf("get properties for bundle %q: %v", bundle.Name, err)
	}
	relatedImages, err := getRelatedImages(bundle)
	if err != nil {
		return nil, fmt.Errorf("get related images for bundle %q: %v", bundle.Name, err)
	}

	var csvJson []byte
	for _, obj := range bundle.Objects {
		if obj.GetKind() == "ClusterServiceVersion" {
			csvJson, err = json.Marshal(obj)
			if err != nil {
				return nil, fmt.Errorf("marshal CSV JSON for bundle %q: %v", bundle.Name, err)
			}
		}
	}

	return &declcfg.Bundle{
		Schema:        "olm.bundle",
		Name:          bundle.Name,
		Package:       bundle.Package,
		Image:         bundle.BundleImage,
		Properties:    props,
		RelatedImages: relatedImages,
		Objects:       objs,
		CsvJSON:       string(csvJson),
	}, nil
}

func getRelatedImages(b *registry.Bundle) ([]declcfg.RelatedImage, error) {
	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return nil, err
	}

	var objmap map[string]*json.RawMessage
	if err = json.Unmarshal(csv.Spec, &objmap); err != nil {
		return nil, err
	}

	var relatedImages []declcfg.RelatedImage
	rawValue, ok := objmap["relatedImages"]
	if ok && rawValue != nil {
		if err = json.Unmarshal(*rawValue, &relatedImages); err != nil {
			return nil, err
		}
	}

	// Keep track of the images we've already found, so that we don't add
	// them multiple times.
	allImages := sets.NewString()
	for _, ri := range relatedImages {
		allImages = allImages.Insert(ri.Image)
	}

	if b.BundleImage != "" && !allImages.Has(b.BundleImage) {
		relatedImages = append(relatedImages, declcfg.RelatedImage{
			Image: b.BundleImage,
		})
	}

	opImages, err := csv.GetOperatorImages()
	if err != nil {
		return nil, err
	}
	for img := range opImages {
		if !allImages.Has(img) {
			relatedImages = append(relatedImages, declcfg.RelatedImage{
				Image: img,
			})
		}
		allImages = allImages.Insert(img)
	}

	return relatedImages, nil
}

func moveBundleObjectsToEndOfPropertySlices(cfg *declcfg.DeclarativeConfig) {
	for bi, b := range cfg.Bundles {
		var (
			others []property.Property
			objs   []property.Property
		)
		for _, p := range b.Properties {
			switch p.Type {
			case property.TypeBundleObject, property.TypeCSVMetadata:
				objs = append(objs, p)
			default:
				others = append(others, p)
			}
		}
		cfg.Bundles[bi].Properties = append(others, objs...)
	}
}

func migrate(cfg *declcfg.DeclarativeConfig) error {
	migrations := []func(*declcfg.DeclarativeConfig) error{
		convertObjectsToCSVMetadata,
	}

	for _, m := range migrations {
		if err := m(cfg); err != nil {
			return err
		}
	}
	return nil
}

func convertObjectsToCSVMetadata(cfg *declcfg.DeclarativeConfig) error {
BundleLoop:
	for bi, b := range cfg.Bundles {
		if b.Image == "" || b.CsvJSON == "" {
			continue
		}

		var csv v1alpha1.ClusterServiceVersion
		if err := json.Unmarshal([]byte(b.CsvJSON), &csv); err != nil {
			return err
		}

		props := b.Properties[:0]
		for _, p := range b.Properties {
			switch p.Type {
			case property.TypeBundleObject:
				// Get rid of the bundle objects
			case property.TypeCSVMetadata:
				// If this bundle already has a CSV metadata
				// property, we won't mutate the bundle at all.
				continue BundleLoop
			default:
				// Keep all of the other properties
				props = append(props, p)
			}
		}
		cfg.Bundles[bi].Properties = append(props, property.MustBuildCSVMetadata(csv))
	}
	return nil
}

func combineConfigs(cfgs []declcfg.DeclarativeConfig) *declcfg.DeclarativeConfig {
	out := &declcfg.DeclarativeConfig{}
	for _, in := range cfgs {
		out.Merge(&in)
	}
	return out
}

func isBundle(entries []os.DirEntry) bool {
	foundManifests := false
	foundMetadata := false
	for _, e := range entries {
		if e.IsDir() {
			switch e.Name() {
			case "manifests":
				foundManifests = true
			case "metadata":
				foundMetadata = true
			}
		}
		if foundMetadata && foundManifests {
			return true
		}
	}
	return false
}

type imageReferenceTemplateData struct {
	Package string
	Name    string
	Version string
}

func (r *Render) renderBundleDirectory(ref string) (*declcfg.DeclarativeConfig, error) {
	img, err := registry.NewImageInput(image.SimpleReference(""), ref)
	if err != nil {
		return nil, err
	}
	if err := r.templateBundleImageRef(img.Bundle); err != nil {
		return nil, fmt.Errorf("failed templating image reference from bundle for %q: %v", ref, err)
	}
	fbcBundle, err := bundleToDeclcfg(img.Bundle)
	if err != nil {
		return nil, err
	}
	return &declcfg.DeclarativeConfig{Bundles: []declcfg.Bundle{*fbcBundle}}, nil
}

func (r *Render) templateBundleImageRef(bundle *registry.Bundle) error {
	if r.ImageRefTemplate == nil {
		return nil
	}

	var pkgProp property.Package
	for _, p := range bundle.Properties {
		if p.Type != property.TypePackage {
			continue
		}
		if err := json.Unmarshal(p.Value, &pkgProp); err != nil {
			return err
		}
		break
	}

	var buf strings.Builder
	tmplInput := imageReferenceTemplateData{
		Package: bundle.Package,
		Name:    bundle.Name,
		Version: pkgProp.Version,
	}
	if err := r.ImageRefTemplate.Execute(&buf, tmplInput); err != nil {
		return err
	}
	bundle.BundleImage = buf.String()
	return nil
}
