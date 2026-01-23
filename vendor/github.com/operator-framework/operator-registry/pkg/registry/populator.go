package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/blang/semver/v4"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/operator-framework/operator-registry/pkg/image"
	libsemver "github.com/operator-framework/operator-registry/pkg/lib/semver"
)

type Dependencies struct {
	RawMessage []map[string]interface{} `json:"dependencies" yaml:"dependencies"`
}

// DirectoryPopulator loads an unpacked operator bundle from a directory into the database.
type DirectoryPopulator struct {
	loader            Load
	graphLoader       GraphLoader
	querier           Query
	imageDirMap       map[image.Reference]string
	overwrittenImages map[string][]string
}

func NewDirectoryPopulator(loader Load, graphLoader GraphLoader, querier Query, imageDirMap map[image.Reference]string, overwrittenImages map[string][]string) *DirectoryPopulator {
	return &DirectoryPopulator{
		loader:            loader,
		graphLoader:       graphLoader,
		querier:           querier,
		imageDirMap:       imageDirMap,
		overwrittenImages: overwrittenImages,
	}
}

func (i *DirectoryPopulator) Populate(mode Mode) error {
	var errs []error
	imagesToAdd := make([]*ImageInput, 0)
	for to, from := range i.imageDirMap {
		imageInput, err := NewImageInput(to, from)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		imagesToAdd = append(imagesToAdd, imageInput)
	}

	if len(errs) > 0 {
		return utilerrors.NewAggregate(errs)
	}

	err := i.loadManifests(imagesToAdd, mode)
	if err != nil {
		return err
	}

	return nil
}

func (i *DirectoryPopulator) globalSanityCheck(imagesToAdd []*ImageInput) error {
	overwrite := len(i.overwrittenImages) > 0
	var errs []error
	images := make(map[string]struct{})
	for _, image := range imagesToAdd {
		images[image.Bundle.BundleImage] = struct{}{}
	}

	attemptedOverwritesPerPackage := map[string]struct{}{}
	for _, image := range imagesToAdd {
		validOverwrite := false
		bundlePaths, err := i.querier.GetBundlePathsForPackage(context.TODO(), image.Bundle.Package)
		if err != nil {
			// Assume that this means that the bundle is empty
			// Or that this is the first time the package is loaded.
			return nil
		}
		for _, bundlePath := range bundlePaths {
			if _, ok := images[bundlePath]; ok {
				errs = append(errs, BundleImageAlreadyAddedErr{ErrorString: fmt.Sprintf("Bundle %s already exists", image.Bundle.BundleImage)})
				continue
			}
		}
		channels, err := i.querier.ListChannels(context.TODO(), image.Bundle.Package)
		if err != nil {
			return err
		}

		for _, channel := range channels {
			bundle, err := i.querier.GetBundle(context.TODO(), image.Bundle.Package, channel, image.Bundle.Name)
			if err != nil {
				// Assume that if we can not find a bundle for the package, channel and or CSV Name that this is safe to add
				continue
			}
			if bundle != nil {
				if !overwrite {
					// raise error that this package + channel + csv combo is already in the db
					errs = append(errs, PackageVersionAlreadyAddedErr{ErrorString: "Bundle already added that provides package and csv"})
					break
				}
				// ensure overwrite is not in the middle of a channel (i.e. nothing replaces it)
				_, err = i.querier.GetBundleThatReplaces(context.TODO(), image.Bundle.Name, image.Bundle.Package, channel)
				if err != nil {
					if err.Error() == fmt.Errorf("no entry found for %s %s", image.Bundle.Package, channel).Error() {
						// overwrite is not replaced by any other bundle
						validOverwrite = true
						continue
					}
					errs = append(errs, err)
					break
				}
				// This bundle is in this channel but is not the head of this channel
				errs = append(errs, OverwriteErr{ErrorString: "Cannot overwrite a bundle that is not at the head of a channel using --overwrite-latest"})
				validOverwrite = false
				break
			}
		}
		if validOverwrite {
			if _, ok := attemptedOverwritesPerPackage[image.Bundle.Package]; ok {
				errs = append(errs, OverwriteErr{ErrorString: "Cannot overwrite more than one bundle at a time for a given package using --overwrite-latest"})
				break
			}
			attemptedOverwritesPerPackage[image.Bundle.Package] = struct{}{}
		}
	}

	if err := i.ValidateEdgeBundlePackage(imagesToAdd); err != nil {
		errs = append(errs, err)
	}
	return utilerrors.NewAggregate(errs)
}

func (i *DirectoryPopulator) loadManifests(imagesToAdd []*ImageInput, mode Mode) error {
	// global sanity checks before insertion
	if err := i.globalSanityCheck(imagesToAdd); err != nil {
		return err
	}

	switch mode {
	case ReplacesMode:
		// TODO: If this succeeds but the add fails there will be a disconnect between
		// the registry and the index. Loading the bundles in a single transactions as
		// described above would allow us to do the removable in that same transaction
		// and ensure that rollback is possible.

		// globalSanityCheck should have verified this to be a head without anything replacing it
		// and that we have a single overwrite per package

		// nolint:nestif
		if len(i.overwrittenImages) > 0 {
			if overwriter, ok := i.loader.(HeadOverwriter); ok {
				// Assume loader has some way to handle overwritten heads if HeadOverwriter isn't implemented explicitly
				for pkg, imgToDelete := range i.overwrittenImages {
					if len(imgToDelete) == 0 {
						continue
					}
					// delete old head bundle and swap it with the previous real bundle in its replaces chain
					if err := overwriter.RemoveOverwrittenChannelHead(pkg, imgToDelete[0]); err != nil {
						return err
					}
				}
			}
		}

		return i.loadManifestsReplaces(imagesToAdd)
	case SemVerMode:
		for _, image := range imagesToAdd {
			if err := i.loadManifestsSemver(image.Bundle, false); err != nil {
				return err
			}
		}
	case SkipPatchMode:
		for _, image := range imagesToAdd {
			if err := i.loadManifestsSemver(image.Bundle, true); err != nil {
				return err
			}
		}
	default:
		//nolint:staticcheck // ST1005: error message is intentionally capitalized
		return fmt.Errorf("Unsupported update mode")
	}

	// Finally let's delete all the old bundles
	if err := i.loader.ClearNonHeadBundles(); err != nil {
		return fmt.Errorf("Error deleting previous bundles: %s", err)
	}

	return nil
}

var packageContextKey = "package"

// ContextWithPackage adds a package value to a context.
func ContextWithPackage(ctx context.Context, pkg string) context.Context {
	// nolint:staticcheck
	return context.WithValue(ctx, packageContextKey, pkg)
}

// PackageFromContext returns the package value of the context if set, returns false if unset.
func PackageFromContext(ctx context.Context) (string, bool) {
	pkg, ok := ctx.Value(packageContextKey).(string)
	return pkg, ok
}

func (i *DirectoryPopulator) loadManifestsReplaces(images []*ImageInput) error {
	packages := map[string][]*Bundle{}
	var errs []error
	for _, img := range images {
		// Add the bundle directly to the store
		if err := i.loader.AddOperatorBundle(img.Bundle); err != nil {
			errs = append(errs, err)
			continue
		}

		packages[img.Bundle.Package] = append(packages[img.Bundle.Package], img.Bundle)
	}

	// Regenerate the upgrade graphs for each package
	for pkg, bundles := range packages {
		// Add any existing bundles into the mix
		ctx := ContextWithPackage(context.TODO(), pkg)
		existing, err := i.querier.ListRegistryBundles(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		packageManifest, err := SemverPackageManifest(append(existing, bundles...))
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if err = i.loader.AddPackageChannels(*packageManifest); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (i *DirectoryPopulator) loadManifestsSemver(bundle *Bundle, skippatch bool) error {
	graph, err := i.graphLoader.Generate(bundle.Package)
	if err != nil && !errors.Is(err, ErrPackageNotInDatabase) {
		return err
	}

	// add to the graph
	bundleLoader := BundleGraphLoader{}
	updatedGraph, err := bundleLoader.AddBundleToGraph(bundle, graph, &AnnotationsFile{Annotations: *bundle.Annotations}, skippatch)
	if err != nil {
		return err
	}

	if err := i.loader.AddBundleSemver(updatedGraph, bundle); err != nil {
		return fmt.Errorf("error loading bundle %s into db: %s", bundle.Name, err)
	}

	return nil
}

// loadOperatorBundle adds the package information to the loader's store
// nolint:unused
func (i *DirectoryPopulator) loadOperatorBundle(manifest PackageManifest, bundle *Bundle) error {
	if manifest.PackageName == "" {
		return nil
	}

	if err := i.loader.AddBundlePackageChannels(manifest, bundle); err != nil {
		return fmt.Errorf("error loading bundle %s into db: %s", bundle.Name, err)
	}

	return nil
}

type bundleVersion struct {
	name    string
	version semver.Version

	// Keep track of the number of times we visit each version so we can tell if a head is contested
	count int
}

// compare returns a value less than one if the receiver arg is less smaller the given version, greater than one if it is larger, and zero if they are equal.
// This comparison follows typical semver precedence rules, with one addition: whenever two versions are equal with the exception of their build-ids, the build-ids are compared using prerelease precedence rules. Further, versions with no build-id are always less than versions with build-ids; e.g. 1.0.0 < 1.0.0+1.
func (b bundleVersion) compare(v bundleVersion) (int, error) {
	return libsemver.BuildIdCompare(b.version, v.version)
}

// SemverPackageManifest generates a PackageManifest from a set of bundles, determining channel heads and the default channel using semver.
// Bundles with the highest version field (according to semver) are chosen as channel heads, and the default channel is taken from the last,
// highest versioned bundle in the entire set to define it.
// The given bundles must all belong to the same package or an error is thrown.
func SemverPackageManifest(bundles []*Bundle) (*PackageManifest, error) {
	heads := map[string]bundleVersion{}

	var (
		pkgName        string
		defaultChannel string
		maxVersion     bundleVersion
	)

	for _, bundle := range bundles {
		if pkgName != "" && pkgName != bundle.Package {
			return nil, fmt.Errorf("more than one package in input")
		}
		pkgName = bundle.Package

		rawVersion, err := bundle.Version()
		if err != nil {
			return nil, fmt.Errorf("error getting bundle %s version: %s", bundle.Name, err)
		}
		if rawVersion == "" {
			// If a version isn't provided by the bundle, give it a dummy zero version
			// The thought is that properly versioned bundles will always be non-zero
			rawVersion = "0.0.0-z"
		}

		version, err := semver.Parse(rawVersion)
		if err != nil {
			return nil, fmt.Errorf("error parsing bundle %s version %s: %s", bundle.Name, rawVersion, err)
		}
		current := bundleVersion{
			name:    bundle.Name,
			version: version,
			count:   1,
		}

		for _, channel := range bundle.Channels {
			head, ok := heads[channel]
			if !ok {
				heads[channel] = current
				continue
			}

			if c, err := current.compare(head); err != nil {
				return nil, err
			} else if c < 0 {
				continue
			} else if c == 0 {
				// We have a duplicate version, add the count
				current.count += head.count
			}

			// Current >= head
			heads[channel] = current
		}

		// Set max if bundle is greater
		if c, err := current.compare(maxVersion); err != nil {
			return nil, err
		} else if c < 0 {
			continue
		} else if c == 0 {
			current.count += maxVersion.count
		}

		// Current >= maxVersion
		maxVersion = current
		if annotations := bundle.Annotations; annotations != nil && annotations.DefaultChannelName != "" {
			// Take it when you can get it
			defaultChannel = annotations.DefaultChannelName
		}
	}

	if maxVersion.count > 1 {
		return nil, fmt.Errorf("more than one bundle with maximum version %s", maxVersion.version)
	}

	pkg := &PackageManifest{
		PackageName:        pkgName,
		DefaultChannelName: defaultChannel,
	}
	defaultFound := len(heads) == 1 && defaultChannel == ""
	for channel, head := range heads {
		if head.count > 1 {
			return nil, fmt.Errorf("more than one potential channel head for %s", channel)
		}
		if len(heads) == 1 {
			// Only one possible default channel
			pkg.DefaultChannelName = channel
		}
		defaultFound = defaultFound || channel == defaultChannel
		pkg.Channels = append(pkg.Channels, PackageChannel{
			Name:           channel,
			CurrentCSVName: head.name,
		})
	}

	if !defaultFound {
		return nil, fmt.Errorf("unable to determine default channel among channel heads: %+v", heads)
	}

	return pkg, nil
}

// DecodeFile decodes the file at a path into the given interface.
func DecodeFile(path string, into interface{}) error {
	if into == nil {
		panic("programmer error: decode destination must be instantiated before decode")
	}

	fileReader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to read file %s: %s", path, err)
	}
	defer fileReader.Close()

	decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)

	return decoder.Decode(into)
}

// ValidateEdgeBundlePackage ensures that all bundles in the input will only skip or replace bundles in the same package.
func (i *DirectoryPopulator) ValidateEdgeBundlePackage(images []*ImageInput) error {
	// track packages for encountered bundles
	expectedBundlePackages := map[string]string{}
	for _, b := range images {
		r, err := b.Bundle.Replaces()
		if err != nil {
			return fmt.Errorf("failed to validate replaces for bundle %s(%s): %v", b.Bundle.Name, b.Bundle.BundleImage, err)
		}

		skipped, err := b.Bundle.Skips()
		if err != nil {
			return fmt.Errorf("failed to validate skipped entries for bundle %s(%s): %v", b.Bundle.Name, b.Bundle.BundleImage, err)
		}

		for _, bndl := range append(skipped, r, b.Bundle.Name) {
			if len(bndl) == 0 {
				continue
			}

			if pkg, ok := expectedBundlePackages[bndl]; ok && pkg != b.Bundle.Package {
				pkgs := []string{pkg, b.Bundle.Package}
				sort.Strings(pkgs)
				return fmt.Errorf("bundle %s must belong to exactly one package, found on: %v", bndl, pkgs)
			}
			expectedBundlePackages[bndl] = b.Bundle.Package
		}
	}
	if len(expectedBundlePackages) == 0 {
		return nil
	}

	pkgs, err := i.querier.ListPackages(context.TODO())
	if err != nil {
		return fmt.Errorf("unable to verify bundle packages: %v", err)
	}
	for _, pkg := range pkgs {
		entries, err := i.querier.GetChannelEntriesFromPackage(context.TODO(), pkg)
		if err != nil {
			return fmt.Errorf("unable to verify bundles for package %v", err)
		}
		for _, b := range entries {
			if bundlePkg, ok := expectedBundlePackages[b.BundleName]; ok && bundlePkg != b.PackageName {
				return fmt.Errorf("bundle %s belongs to package %s on index, cannot be added as an edge for package %s", b.BundleName, b.PackageName, bundlePkg)
			}
		}
	}

	return nil
}
