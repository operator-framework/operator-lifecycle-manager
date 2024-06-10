package indexer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	"github.com/operator-framework/operator-registry/pkg/image/execregistry"
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"github.com/operator-framework/operator-registry/pkg/lib/certs"
	"github.com/operator-framework/operator-registry/pkg/lib/registry"
	pregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

const (
	defaultDockerfileName     = "index.Dockerfile"
	defaultImageTag           = "operator-registry-index:latest"
	defaultDatabaseFolder     = "database"
	defaultDatabaseFile       = "index.db"
	tmpDirPrefix              = "index_tmp_"
	tmpBuildDirPrefix         = "index_build_tmp"
	concurrencyLimitForExport = 10
)

var ErrFileBasedCatalogPrune = errors.New("`opm index prune` only supports sqlite-based catalogs. See https://github.com/redhat-openshift-ecosystem/community-operators-prod/issues/793 for instructions on pruning a plaintext files backed catalog.")

// ImageIndexer is a struct implementation of the Indexer interface
type ImageIndexer struct {
	DockerfileGenerator    containertools.DockerfileGenerator
	CommandRunner          containertools.CommandRunner
	LabelReader            containertools.LabelReader
	RegistryAdder          registry.RegistryAdder
	RegistryDeleter        registry.RegistryDeleter
	RegistryPruner         registry.RegistryPruner
	RegistryStrandedPruner registry.RegistryStrandedPruner
	RegistryDeprecator     registry.RegistryDeprecator
	BuildTool              containertools.ContainerTool
	PullTool               containertools.ContainerTool
	Logger                 *logrus.Entry
}

// AddToIndexRequest defines the parameters to send to the AddToIndex API
type AddToIndexRequest struct {
	Generate          bool
	Permissive        bool
	BinarySourceImage string
	FromIndex         string
	OutDockerfile     string
	Bundles           []string
	Tag               string
	Mode              pregistry.Mode
	CaFile            string
	SkipTLSVerify     bool
	PlainHTTP         bool
	Overwrite         bool
	EnableAlpha       bool
}

// AddToIndex is an aggregate API used to generate a registry index image with additional bundles
func (i ImageIndexer) AddToIndex(request AddToIndexRequest) error {
	buildDir, outDockerfile, cleanup, err := buildContext(request.Generate, request.OutDockerfile)
	defer cleanup()
	if err != nil {
		return err
	}

	databasePath, err := i.ExtractDatabase(buildDir, request.FromIndex, request.CaFile, request.SkipTLSVerify, request.PlainHTTP)
	if err != nil {
		return err
	}

	// Run opm registry add on the database
	addToRegistryReq := registry.AddToRegistryRequest{
		Bundles:       request.Bundles,
		InputDatabase: databasePath,
		Permissive:    request.Permissive,
		Mode:          request.Mode,
		SkipTLSVerify: request.SkipTLSVerify,
		PlainHTTP:     request.PlainHTTP,
		ContainerTool: i.PullTool,
		Overwrite:     request.Overwrite,
		EnableAlpha:   request.EnableAlpha,
	}

	// Add the bundles to the registry
	err = i.RegistryAdder.AddToRegistry(addToRegistryReq)
	if err != nil {
		i.Logger.WithError(err).Debugf("unable to add bundle to registry")
		return err
	}

	// generate the dockerfile
	dockerfile := i.DockerfileGenerator.GenerateIndexDockerfile(request.BinarySourceImage, databasePath)
	err = write(dockerfile, outDockerfile, i.Logger)
	if err != nil {
		return err
	}

	if request.Generate {
		return nil
	}

	// build the dockerfile
	err = build(outDockerfile, request.Tag, i.CommandRunner, i.Logger)
	if err != nil {
		return err
	}

	return nil
}

// DeleteFromIndexRequest defines the parameters to send to the DeleteFromIndex API
type DeleteFromIndexRequest struct {
	Generate          bool
	Permissive        bool
	BinarySourceImage string
	FromIndex         string
	OutDockerfile     string
	Tag               string
	Operators         []string
	SkipTLSVerify     bool
	PlainHTTP         bool
	CaFile            string
}

// DeleteFromIndex is an aggregate API used to generate a registry index image
// without specific operators
func (i ImageIndexer) DeleteFromIndex(request DeleteFromIndexRequest) error {
	buildDir, outDockerfile, cleanup, err := buildContext(request.Generate, request.OutDockerfile)
	defer cleanup()
	if err != nil {
		return err
	}

	databasePath, err := i.ExtractDatabase(buildDir, request.FromIndex, request.CaFile, request.SkipTLSVerify, request.PlainHTTP)
	if err != nil {
		return err
	}

	// Run opm registry delete on the database
	deleteFromRegistryReq := registry.DeleteFromRegistryRequest{
		Packages:      request.Operators,
		InputDatabase: databasePath,
		Permissive:    request.Permissive,
	}

	// Delete the bundles from the registry
	err = i.RegistryDeleter.DeleteFromRegistry(deleteFromRegistryReq)
	if err != nil {
		return err
	}

	// generate the dockerfile
	dockerfile := i.DockerfileGenerator.GenerateIndexDockerfile(request.BinarySourceImage, databasePath)
	err = write(dockerfile, outDockerfile, i.Logger)
	if err != nil {
		return err
	}

	if request.Generate {
		return nil
	}

	// build the dockerfile
	err = build(outDockerfile, request.Tag, i.CommandRunner, i.Logger)
	if err != nil {
		return err
	}

	return nil
}

// PruneStrandedFromIndexRequest defines the parameters to send to the PruneStrandedFromIndex API
type PruneStrandedFromIndexRequest struct {
	Generate          bool
	BinarySourceImage string
	FromIndex         string
	OutDockerfile     string
	Tag               string
	CaFile            string
	SkipTLSVerify     bool
	PlainHTTP         bool
}

// PruneStrandedFromIndex is an aggregate API used to generate a registry index image
// that has removed stranded bundles from the index
func (i ImageIndexer) PruneStrandedFromIndex(request PruneStrandedFromIndexRequest) error {
	buildDir, outDockerfile, cleanup, err := buildContext(request.Generate, request.OutDockerfile)
	defer cleanup()
	if err != nil {
		return err
	}

	databasePath, err := i.ExtractDatabase(buildDir, request.FromIndex, request.CaFile, request.SkipTLSVerify, request.PlainHTTP)
	if err != nil {
		return err
	}

	// Run opm registry prune-stranded on the database
	pruneStrandedFromRegistryReq := registry.PruneStrandedFromRegistryRequest{
		InputDatabase: databasePath,
	}

	// Delete the stranded bundles from the registry
	err = i.RegistryStrandedPruner.PruneStrandedFromRegistry(pruneStrandedFromRegistryReq)
	if err != nil {
		return err
	}

	// generate the dockerfile
	dockerfile := i.DockerfileGenerator.GenerateIndexDockerfile(request.BinarySourceImage, databasePath)
	err = write(dockerfile, outDockerfile, i.Logger)
	if err != nil {
		return err
	}

	if request.Generate {
		return nil
	}

	// build the dockerfile
	err = build(outDockerfile, request.Tag, i.CommandRunner, i.Logger)
	if err != nil {
		return err
	}
	return nil
}

// PruneFromIndexRequest defines the parameters to send to the PruneFromIndex API
type PruneFromIndexRequest struct {
	Generate          bool
	Permissive        bool
	BinarySourceImage string
	FromIndex         string
	OutDockerfile     string
	Tag               string
	Packages          []string
	CaFile            string
	SkipTLSVerify     bool
	PlainHTTP         bool
}

func (i ImageIndexer) PruneFromIndex(request PruneFromIndexRequest) error {
	buildDir, outDockerfile, cleanup, err := buildContext(request.Generate, request.OutDockerfile)
	defer cleanup()
	if err != nil {
		return err
	}

	databasePath, err := i.ExtractDatabase(buildDir, request.FromIndex, request.CaFile, request.SkipTLSVerify, request.PlainHTTP)
	if err != nil {
		return err
	}

	// Run opm registry prune on the database
	pruneFromRegistryReq := registry.PruneFromRegistryRequest{
		Packages:      request.Packages,
		InputDatabase: databasePath,
		Permissive:    request.Permissive,
	}

	// Prune the bundles from the registry
	err = i.RegistryPruner.PruneFromRegistry(pruneFromRegistryReq)
	if err != nil {
		return err
	}

	// generate the dockerfile
	dockerfile := i.DockerfileGenerator.GenerateIndexDockerfile(request.BinarySourceImage, databasePath)
	err = write(dockerfile, outDockerfile, i.Logger)
	if err != nil {
		return err
	}

	if request.Generate {
		return nil
	}

	// build the dockerfile
	err = build(outDockerfile, request.Tag, i.CommandRunner, i.Logger)
	if err != nil {
		return err
	}

	return nil
}

// ExtractDatabase sets a temp directory for unpacking an image
func (i ImageIndexer) ExtractDatabase(buildDir, fromIndex, caFile string, skipTLSVerify, plainHTTP bool) (string, error) {
	tmpDir, err := os.MkdirTemp("./", tmpDirPrefix)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	databaseFile, err := i.getDatabaseFile(tmpDir, fromIndex, caFile, skipTLSVerify, plainHTTP)
	if err != nil {
		return "", err
	}
	// copy the index to the database folder in the build directory
	return copyDatabaseTo(databaseFile, filepath.Join(buildDir, defaultDatabaseFolder))
}

func (i ImageIndexer) getDatabaseFile(workingDir, fromIndex, caFile string, skipTLSVerify, plainHTTP bool) (string, error) {
	if fromIndex == "" {
		return path.Join(workingDir, defaultDatabaseFile), nil
	}

	// Pull the fromIndex
	i.Logger.Infof("Pulling previous image %s to get metadata", fromIndex)

	var reg image.Registry
	var rerr error
	switch i.PullTool {
	case containertools.NoneTool:
		rootCAs, err := certs.RootCAs(caFile)
		if err != nil {
			return "", fmt.Errorf("failed to get RootCAs: %v", err)
		}
		reg, rerr = containerdregistry.NewRegistry(
			containerdregistry.SkipTLSVerify(skipTLSVerify),
			containerdregistry.WithPlainHTTP(plainHTTP),
			containerdregistry.WithLog(i.Logger),
			containerdregistry.WithRootCAs(rootCAs))
	case containertools.PodmanTool:
		fallthrough
	case containertools.DockerTool:
		reg, rerr = execregistry.NewRegistry(i.PullTool, i.Logger, containertools.SkipTLS(plainHTTP))
	}
	if rerr != nil {
		return "", rerr
	}
	defer func() {
		if err := reg.Destroy(); err != nil {
			i.Logger.WithError(err).Warn("error destroying local cache")
		}
	}()

	imageRef := image.SimpleReference(fromIndex)

	if err := reg.Pull(context.TODO(), imageRef); err != nil {
		return "", err
	}

	// Get the old index image's dbLocationLabel to find this path
	labels, err := reg.Labels(context.TODO(), imageRef)
	if err != nil {
		return "", err
	}

	dbLocation, ok := labels[containertools.DbLocationLabel]
	if !ok {
		if _, ok := labels[containertools.ConfigsLocationLabel]; ok {
			return "", ErrFileBasedCatalogPrune
		}
		return "", fmt.Errorf("index image %s missing label %s", fromIndex, containertools.DbLocationLabel)
	}

	if err := reg.Unpack(context.TODO(), imageRef, workingDir); err != nil {
		return "", err
	}

	return path.Join(workingDir, dbLocation), nil
}

func copyDatabaseTo(databaseFile, targetDir string) (string, error) {
	// create the containing folder if it doesn't exist
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		if err := os.MkdirAll(targetDir, 0777); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}

	// Open the database file in the working dir
	from, err := os.OpenFile(databaseFile, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return "", err
	}
	defer from.Close()

	dbFile := path.Join(targetDir, defaultDatabaseFile)

	// define the path to copy to the database/index.db file
	to, err := os.OpenFile(dbFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return "", err
	}
	defer to.Close()

	// copy to the destination directory
	_, err = io.Copy(to, from)
	return to.Name(), err
}

func buildContext(generate bool, requestedDockerfile string) (buildDir, outDockerfile string, cleanup func(), err error) {
	// set cleanup to a no-op until explicitly set
	cleanup = func() {}

	if generate {
		buildDir = "./"
		if len(requestedDockerfile) == 0 {
			outDockerfile = defaultDockerfileName
		} else {
			outDockerfile = requestedDockerfile
		}
		cleanup = func() {}
		return
	}

	// set a temp directory for building the new image
	buildDir, err = os.MkdirTemp(".", tmpBuildDirPrefix)
	if err != nil {
		return
	}
	cleanup = func() {
		os.RemoveAll(buildDir)
	}

	if len(requestedDockerfile) > 0 {
		outDockerfile = requestedDockerfile
		return
	}

	// generate a temp dockerfile if needed
	tempDockerfile, err := os.CreateTemp(".", defaultDockerfileName)
	if err != nil {
		defer cleanup()
		return
	}
	outDockerfile = tempDockerfile.Name()
	cleanup = func() {
		os.RemoveAll(buildDir)
		os.Remove(outDockerfile)
	}

	return
}

func build(dockerfilePath, imageTag string, commandRunner containertools.CommandRunner, logger *logrus.Entry) error {
	if imageTag == "" {
		imageTag = defaultImageTag
	}

	logger.Debugf("building container image: %s", imageTag)

	err := commandRunner.Build(dockerfilePath, imageTag)
	if err != nil {
		return err
	}

	return nil
}

func write(dockerfileText, outDockerfile string, logger *logrus.Entry) error {
	if outDockerfile == "" {
		outDockerfile = defaultDockerfileName
	}

	logger.Infof("writing dockerfile: %s", outDockerfile)

	f, err := os.Create(outDockerfile)
	if err != nil {
		return err
	}

	_, err = f.WriteString(dockerfileText)
	if err != nil {
		return err
	}

	return nil
}

// ExportFromIndexRequest defines the parameters to send to the ExportFromIndex API
type ExportFromIndexRequest struct {
	Index         string
	Packages      []string
	DownloadPath  string
	ContainerTool containertools.ContainerTool
	CaFile        string
	SkipTLSVerify bool
	PlainHTTP     bool
}

// ExportFromIndex is an aggregate API used to specify operators from
// an index image
func (i ImageIndexer) ExportFromIndex(request ExportFromIndexRequest) error {
	// set a temp directory
	workingDir, err := os.MkdirTemp("./", tmpDirPrefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(workingDir)

	// extract the index database to the file
	databaseFile, err := i.getDatabaseFile(workingDir, request.Index, request.CaFile, request.SkipTLSVerify, request.PlainHTTP)
	if err != nil {
		return err
	}

	db, err := sqlite.Open(databaseFile)
	if err != nil {
		return err
	}
	defer db.Close()

	dbQuerier := sqlite.NewSQLLiteQuerierFromDb(db)

	// fetch all packages from the index image if packages is empty
	if len(request.Packages) == 0 {
		request.Packages, err = dbQuerier.ListPackages(context.TODO())
		if err != nil {
			return err
		}
	}

	bundles, err := getBundlesToExport(dbQuerier, request.Packages)
	if err != nil {
		return err
	}

	i.Logger.Infof("Preparing to pull bundles %+q", bundles)

	// Creating downloadPath dir
	if err := os.MkdirAll(request.DownloadPath, 0777); err != nil {
		return err
	}

	var errs []error
	var wg sync.WaitGroup
	wg.Add(len(bundles))
	var mu = &sync.Mutex{}

	sem := make(chan struct{}, concurrencyLimitForExport)

	for bundleImage, bundleDir := range bundles {
		go func(bundleImage string, bundleDir bundleDirPrefix) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() {
				<-sem
			}()

			// generate a random folder name if bundle version is empty
			if bundleDir.bundleVersion == "" {
				bundleDir.bundleVersion = strconv.Itoa(rand.Intn(10000))
			}
			exporter := bundle.NewExporterForBundle(bundleImage, filepath.Join(request.DownloadPath, bundleDir.pkgName, bundleDir.bundleVersion), request.ContainerTool)
			if err := exporter.Export(request.SkipTLSVerify, request.PlainHTTP); err != nil {
				err = fmt.Errorf("exporting bundle image:%s failed with %s", bundleImage, err)
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(bundleImage, bundleDir)
	}
	// Wait for all the go routines to finish export
	wg.Wait()

	if errs != nil {
		return utilerrors.NewAggregate(errs)
	}

	for _, packageName := range request.Packages {
		err := generatePackageYaml(dbQuerier, packageName, filepath.Join(request.DownloadPath, packageName))
		if err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}

type bundleDirPrefix struct {
	pkgName, bundleVersion string
}

func getBundlesToExport(dbQuerier pregistry.Query, packages []string) (map[string]bundleDirPrefix, error) {
	bundleMap := make(map[string]bundleDirPrefix)

	for _, packageName := range packages {
		bundlesForPackage, err := dbQuerier.GetBundlesForPackage(context.TODO(), packageName)
		if err != nil {
			return nil, err
		}
		for k, _ := range bundlesForPackage {
			bundleMap[k.BundlePath] = bundleDirPrefix{pkgName: packageName, bundleVersion: k.Version}
		}
	}

	return bundleMap, nil
}

func generatePackageYaml(dbQuerier pregistry.Query, packageName, downloadPath string) error {
	var errs []error

	defaultChannel, err := dbQuerier.GetDefaultChannelForPackage(context.TODO(), packageName)
	if err != nil {
		return err
	}

	channelList, err := dbQuerier.ListChannels(context.TODO(), packageName)
	if err != nil {
		return err
	}

	channels := []pregistry.PackageChannel{}
	for _, ch := range channelList {
		csvName, err := dbQuerier.GetCurrentCSVNameForChannel(context.TODO(), packageName, ch)
		if err != nil {
			err = fmt.Errorf("error exporting bundle from image: %s", err)
			errs = append(errs, err)
			continue
		}
		channels = append(channels,
			pregistry.PackageChannel{
				Name:           ch,
				CurrentCSVName: csvName,
			})
	}

	manifest := pregistry.PackageManifest{
		PackageName:        packageName,
		DefaultChannelName: defaultChannel,
		Channels:           channels,
	}

	manifestBytes, err := yaml.Marshal(&manifest)
	if err != nil {
		errs = append(errs, err)
		return utilerrors.NewAggregate(errs)
	}

	err = bundle.WriteFile("package.yaml", downloadPath, manifestBytes)
	if err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

// DeprecateFromIndexRequest defines the parameters to send to the PruneFromIndex API
type DeprecateFromIndexRequest struct {
	Generate            bool
	Permissive          bool
	BinarySourceImage   string
	FromIndex           string
	OutDockerfile       string
	Bundles             []string
	Tag                 string
	CaFile              string
	SkipTLSVerify       bool
	PlainHTTP           bool
	AllowPackageRemoval bool
}

// DeprecateFromIndex takes a DeprecateFromIndexRequest and deprecates the requested
// bundles.
func (i ImageIndexer) DeprecateFromIndex(request DeprecateFromIndexRequest) error {
	buildDir, outDockerfile, cleanup, err := buildContext(request.Generate, request.OutDockerfile)
	defer cleanup()
	if err != nil {
		return err
	}

	databasePath, err := i.ExtractDatabase(buildDir, request.FromIndex, request.CaFile, request.SkipTLSVerify, request.PlainHTTP)
	if err != nil {
		return err
	}

	deprecateFromRegistryReq := registry.DeprecateFromRegistryRequest{
		Bundles:             request.Bundles,
		InputDatabase:       databasePath,
		Permissive:          request.Permissive,
		AllowPackageRemoval: request.AllowPackageRemoval,
	}

	// Deprecate the bundles from the registry
	err = i.RegistryDeprecator.DeprecateFromRegistry(deprecateFromRegistryReq)
	if err != nil {
		return err
	}

	// generate the dockerfile
	dockerfile := i.DockerfileGenerator.GenerateIndexDockerfile(request.BinarySourceImage, databasePath)
	err = write(dockerfile, outDockerfile, i.Logger)
	if err != nil {
		return err
	}

	if request.Generate {
		return nil
	}

	// build the dockerfile with requested tooling
	err = build(outDockerfile, request.Tag, i.CommandRunner, i.Logger)
	if err != nil {
		return err
	}

	return nil
}
