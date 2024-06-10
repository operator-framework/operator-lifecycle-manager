package registry

import (
	"context"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	"github.com/operator-framework/operator-registry/pkg/image/execregistry"
	"github.com/operator-framework/operator-registry/pkg/lib/certs"
	"github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

type RegistryUpdater struct {
	Logger *logrus.Entry
}

type AddToRegistryRequest struct {
	Permissive    bool
	SkipTLSVerify bool
	PlainHTTP     bool
	CaFile        string
	InputDatabase string
	Bundles       []string
	Mode          registry.Mode
	ContainerTool containertools.ContainerTool
	Overwrite     bool
	EnableAlpha   bool
}

func (r RegistryUpdater) AddToRegistry(request AddToRegistryRequest) error {
	db, err := sqlite.Open(request.InputDatabase)
	if err != nil {
		return err
	}
	defer db.Close()

	dbLoader, err := sqlite.NewSQLLiteLoader(db, sqlite.WithEnableAlpha(request.EnableAlpha))
	if err != nil {
		return err
	}

	if err := dbLoader.Migrate(context.TODO()); err != nil {
		return err
	}

	graphLoader, err := sqlite.NewSQLGraphLoaderFromDB(db)
	if err != nil {
		return err
	}
	dbQuerier := sqlite.NewSQLLiteQuerierFromDb(db)

	// add custom ca certs to resolver

	var reg image.Registry
	var rerr error
	switch request.ContainerTool {
	case containertools.NoneTool:
		rootCAs, err := certs.RootCAs(request.CaFile)
		if err != nil {
			return fmt.Errorf("failed to get RootCAs: %v", err)
		}
		reg, rerr = containerdregistry.NewRegistry(
			containerdregistry.SkipTLSVerify(request.SkipTLSVerify),
			containerdregistry.WithPlainHTTP(request.PlainHTTP),
			containerdregistry.WithRootCAs(rootCAs),
		)
	case containertools.PodmanTool:
		fallthrough
	case containertools.DockerTool:
		reg, rerr = execregistry.NewRegistry(request.ContainerTool, r.Logger, containertools.SkipTLS(request.PlainHTTP))
	}
	if rerr != nil {
		return rerr
	}
	defer func() {
		if err := reg.Destroy(); err != nil {
			r.Logger.WithError(err).Warn("error destroying local cache")
		}
	}()

	simpleRefs := make([]image.Reference, 0)
	for _, ref := range request.Bundles {
		simpleRefs = append(simpleRefs, image.SimpleReference(ref))
	}

	if err := populate(context.TODO(), dbLoader, graphLoader, dbQuerier, reg, simpleRefs, request.Mode, request.Overwrite); err != nil {
		r.Logger.Debugf("unable to populate database: %s", err)

		if !request.Permissive {
			r.Logger.WithError(err).Error("permissive mode disabled")
			return err
		}
		r.Logger.WithError(err).Warn("permissive mode enabled")
	}

	return nil
}

func unpackImage(ctx context.Context, reg image.Registry, ref image.Reference) (image.Reference, string, func(), error) {
	var errs []error
	workingDir, err := os.MkdirTemp("./", "bundle_tmp")
	if err != nil {
		errs = append(errs, err)
	}

	if err = reg.Pull(ctx, ref); err != nil {
		errs = append(errs, err)
	}

	if err = reg.Unpack(ctx, ref, workingDir); err != nil {
		errs = append(errs, err)
	}

	cleanup := func() {
		if err := os.RemoveAll(workingDir); err != nil {
			logrus.Error(err)
		}
	}

	if len(errs) > 0 {
		return nil, "", cleanup, utilerrors.NewAggregate(errs)
	}
	return ref, workingDir, cleanup, nil
}

func populate(ctx context.Context, loader registry.Load, graphLoader registry.GraphLoader, querier registry.Query, reg image.Registry, refs []image.Reference, mode registry.Mode, overwrite bool) error {
	unpackedImageMap := make(map[image.Reference]string, 0)
	overwrittenBundles := map[string][]string{}
	var imagesToAdd []*registry.Bundle
	for _, ref := range refs {
		to, from, cleanup, err := unpackImage(ctx, reg, ref)
		if err != nil {
			return err
		}
		unpackedImageMap[to] = from
		defer cleanup()

		img, err := registry.NewImageInput(to, from)
		if err != nil {
			return err
		}
		imagesToAdd = append(imagesToAdd, img.Bundle)

		if overwrite {
			overwritten, err := querier.GetBundlePathIfExists(ctx, img.Bundle.Name)
			if err != nil {
				if err == registry.ErrBundleImageNotInDatabase {
					continue
				}
				return err
			}
			if overwritten == "" {
				return fmt.Errorf("index add --overwrite-latest is only supported when using bundle images")
			}
			overwrittenBundles[img.Bundle.Package] = append(overwrittenBundles[img.Bundle.Package], img.Bundle.Name)
		}
	}

	populator := registry.NewDirectoryPopulator(loader, graphLoader, querier, unpackedImageMap, overwrittenBundles)

	if err := populator.Populate(mode); err != nil {

		return err

	}
	return checkForBundles(ctx, querier.(*sqlite.SQLQuerier), graphLoader, imagesToAdd)
}

type DeleteFromRegistryRequest struct {
	Permissive    bool
	InputDatabase string
	Packages      []string
}

func (r RegistryUpdater) DeleteFromRegistry(request DeleteFromRegistryRequest) error {
	db, err := sqlite.Open(request.InputDatabase)
	if err != nil {
		return err
	}
	defer db.Close()

	dbLoader, err := sqlite.NewDeprecationAwareLoader(db)
	if err != nil {
		return err
	}
	if err := dbLoader.Migrate(context.TODO()); err != nil {
		return err
	}

	for _, pkg := range request.Packages {
		remover := sqlite.NewSQLRemoverForPackages(dbLoader, pkg)
		if err := remover.Remove(); err != nil {
			err = fmt.Errorf("error deleting packages from database: %s", err)
			if !request.Permissive {
				logrus.WithError(err).Fatal("permissive mode disabled")
				return err
			}
			logrus.WithError(err).Warn("permissive mode enabled")
		}
	}

	// remove any stranded bundles from the database
	// TODO: This is unnecessary if the db schema can prevent this orphaned data from existing
	remover := sqlite.NewSQLStrandedBundleRemover(dbLoader)
	if err := remover.Remove(); err != nil {
		return fmt.Errorf("error removing stranded packages from database: %s", err)
	}

	if _, err := db.Exec("VACUUM"); err != nil {
		return err
	}
	return nil
}

type PruneStrandedFromRegistryRequest struct {
	InputDatabase string
}

func (r RegistryUpdater) PruneStrandedFromRegistry(request PruneStrandedFromRegistryRequest) error {
	db, err := sqlite.Open(request.InputDatabase)
	if err != nil {
		return err
	}
	defer db.Close()

	dbLoader, err := sqlite.NewSQLLiteLoader(db)
	if err != nil {
		return err
	}
	if err := dbLoader.Migrate(context.TODO()); err != nil {
		return err
	}

	remover := sqlite.NewSQLStrandedBundleRemover(dbLoader)
	if err := remover.Remove(); err != nil {
		return fmt.Errorf("error removing stranded packages from database: %s", err)
	}

	if _, err := db.Exec("VACUUM"); err != nil {
		return err
	}
	return nil
}

type PruneFromRegistryRequest struct {
	Permissive    bool
	InputDatabase string
	Packages      []string
}

func (r RegistryUpdater) PruneFromRegistry(request PruneFromRegistryRequest) error {
	db, err := sqlite.Open(request.InputDatabase)
	if err != nil {
		return err
	}
	defer db.Close()

	dbLoader, err := sqlite.NewDeprecationAwareLoader(db)
	if err != nil {
		return err
	}
	if err := dbLoader.Migrate(context.TODO()); err != nil {
		return err
	}

	// get all the packages
	lister := sqlite.NewSQLLiteQuerierFromDb(db)
	packages, err := lister.ListPackages(context.TODO())
	if err != nil {
		return err
	}

	// make it inexpensive to find packages
	pkgMap := make(map[string]bool)
	for _, pkg := range request.Packages {
		pkgMap[pkg] = true
	}

	// prune packages from registry
	for _, pkg := range packages {
		if _, found := pkgMap[pkg]; !found {
			remover := sqlite.NewSQLRemoverForPackages(dbLoader, pkg)
			if err := remover.Remove(); err != nil {
				err = fmt.Errorf("error deleting packages from database: %s", err)
				if !request.Permissive {
					logrus.WithError(err).Fatal("permissive mode disabled")
					return err
				}
				logrus.WithError(err).Warn("permissive mode enabled")
			}
		}
	}

	if _, err := db.Exec("VACUUM"); err != nil {
		return err
	}
	return nil
}

type DeprecateFromRegistryRequest struct {
	Permissive          bool
	InputDatabase       string
	Bundles             []string
	AllowPackageRemoval bool
}

func (r RegistryUpdater) DeprecateFromRegistry(request DeprecateFromRegistryRequest) error {
	db, err := sqlite.Open(request.InputDatabase)
	if err != nil {
		return err
	}
	defer db.Close()

	dbLoader, err := sqlite.NewDeprecationAwareLoader(db)
	if err != nil {
		return err
	}
	if err := dbLoader.Migrate(context.TODO()); err != nil {
		return fmt.Errorf("unable to migrate database: %s", err)
	}

	// Check if all bundlepaths are valid
	var toDeprecate []string

	dbQuerier := sqlite.NewSQLLiteQuerierFromDb(db)

	toDeprecate, _, err = checkForBundlePaths(dbQuerier, request.Bundles)
	if err != nil {
		if !request.Permissive {
			r.Logger.WithError(err).Error("permissive mode disabled")
			return err
		}
		r.Logger.WithError(err).Warn("permissive mode enabled")
	}

	deprecator := sqlite.NewSQLDeprecatorForBundles(dbLoader, toDeprecate)

	// Check for deprecation of head of default channel. If deprecation request includes heads of all other channels,
	// then remove the package entirely. Otherwise, deprecate provided bundles. This enables deprecating an entire package.
	// By default deprecating the head of default channel is not permitted.
	if request.AllowPackageRemoval {
		packageDeprecator := sqlite.NewSQLDeprecatorForBundlesAndPackages(deprecator, dbQuerier)
		if err := packageDeprecator.MaybeRemovePackages(); err != nil {
			r.Logger.Debugf("unable to deprecate package from database: %s", err)
			if !request.Permissive {
				r.Logger.WithError(err).Error("permissive mode disabled")
				return err
			}
			r.Logger.WithError(err).Warn("permissive mode enabled")
		}
	}

	// Any bundles associated with removed packages are now removed from the list of bundles to deprecate.
	if err := deprecator.Deprecate(); err != nil {
		r.Logger.Debugf("unable to deprecate bundles from database: %s", err)
		if !request.Permissive {
			r.Logger.WithError(err).Error("permissive mode disabled")
			return err
		}
		r.Logger.WithError(err).Warn("permissive mode enabled")
	}

	if _, err := db.Exec("VACUUM"); err != nil {
		return err
	}
	return nil
}

// checkForBundlePaths verifies presence of a list of bundle paths in the registry.
func checkForBundlePaths(querier registry.GRPCQuery, bundlePaths []string) ([]string, []string, error) {
	if len(bundlePaths) == 0 {
		return bundlePaths, nil, nil
	}

	registryBundles, err := querier.ListBundles(context.TODO())
	if err != nil {
		return bundlePaths, nil, err
	}

	if len(registryBundles) == 0 {
		return nil, bundlePaths, nil
	}

	registryBundlePaths := map[string]struct{}{}
	for _, b := range registryBundles {
		registryBundlePaths[b.BundlePath] = struct{}{}
	}

	var found, missing []string
	for _, b := range bundlePaths {
		if _, ok := registryBundlePaths[b]; ok {
			found = append(found, b)
			continue
		}
		missing = append(missing, b)
	}
	if len(missing) > 0 {
		return found, missing, fmt.Errorf("target bundlepaths for deprecation missing from registry: %v", missing)
	}
	return found, missing, nil
}

// replaces mode selects highest version as channel head and
// prunes any bundles in the upgrade chain after the channel head.
// check for the presence of newly added bundles after a replaces-mode add.
func checkForBundles(ctx context.Context, q *sqlite.SQLQuerier, g registry.GraphLoader, required []*registry.Bundle) error {
	var errs []error
	for _, bundle := range required {
		graph, err := g.Generate(bundle.Package)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to verify added bundles for package %s: %v", bundle.Package, err))
			continue
		}

		for _, channel := range bundle.Channels {
			var foundImage bool
			for next := []registry.BundleKey{graph.Channels[channel].Head}; len(next) > 0; next = next[1:] {
				if next[0].BundlePath == bundle.BundleImage {
					foundImage = true
					break
				}
				for edge := range graph.Channels[channel].Nodes[next[0]] {
					next = append(next, edge)
				}
			}

			if foundImage {
				continue
			}

			var headSkips []string
			for b := range graph.Channels[channel].Nodes[graph.Channels[channel].Head] {
				headSkips = append(headSkips, b.CsvName)
			}
			errs = append(errs, fmt.Errorf("add prunes bundle %s (%s) from package %s, channel %s: this may be "+
				"due to incorrect channel head (%s, skips/replaces %v). "+
				"Be aware that the head of the channel %s where you are trying to add the %s is %s. "+
				"Upgrade graphs follows the Semantic Versioning 2.0.0 (https://semver.org/) which means that "+
				"is not possible add new versions lower then the head of the channel",
				bundle.Name,
				bundle.BundleImage,
				bundle.Package,
				channel,
				graph.Channels[channel].Head.CsvName,
				headSkips,
				channel,
				bundle.Name,
				graph.Channels[channel].Head.CsvName))
		}
	}
	return utilerrors.NewAggregate(errs)
}
