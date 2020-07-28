package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	operatorslisters "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	registrygrpc "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/grpc"
	utillabels "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/labels"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators"
	pkglisters "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/listers/operators/internalversion"
	"github.com/operator-framework/operator-registry/pkg/api"
)

const (
	catalogIndex = "catalog"
	cacheTimeout = 5 * time.Minute
	readyTimeout = 10 * time.Minute
	stateTimeout = 20 * time.Second
)

func getSourceKey(pkg *operators.PackageManifest) (key *registry.CatalogKey) {
	if pkg != nil {
		key = &registry.CatalogKey{
			Namespace: pkg.Status.CatalogSourceNamespace,
			Name:      pkg.Status.CatalogSource,
		}
	}

	return
}

func catalogIndexFunc(obj interface{}) ([]string, error) {
	pkg, ok := obj.(*operators.PackageManifest)
	if !ok {
		return []string{""}, fmt.Errorf("obj is not a packagemanifest %v", obj)
	}

	return []string{getSourceKey(pkg).String()}, nil
}

func PackageManifestKeyFunc(obj interface{}) (string, error) {
	if key, ok := obj.(string); ok {
		return string(key), nil
	}

	pkg, ok := obj.(*operators.PackageManifest)
	if !ok {
		return "", fmt.Errorf("obj is not a packagemanifest %v", obj)
	}

	return pkg.Status.CatalogSource + "/" + pkg.GetNamespace() + "/" + pkg.GetName(), nil
}

func SplitPackageManifestKey(key string) (catsrcname, namespace, name string, err error) {
	parts := strings.Split(key, "/")
	switch len(parts) {
	case 3:
		// catalogsource name, namespace and packagemanifest name
		return parts[0], parts[1], parts[2], nil
	}

	return "", "", "", fmt.Errorf("unexpected key format: %q", key)
}

type registryClient struct {
	api.RegistryClient
	catsrc *operatorsv1alpha1.CatalogSource
	conn   *grpc.ClientConn
}

func newRegistryClient(catsrc *operatorsv1alpha1.CatalogSource, conn *grpc.ClientConn) *registryClient {
	return &registryClient{
		RegistryClient: api.NewRegistryClient(conn),
		catsrc:         catsrc,
		conn:           conn,
	}
}

func (r *registryClient) key() (key registry.CatalogKey, err error) {
	if r.catsrc == nil {
		err = fmt.Errorf("cannot get key, nil catalog")
		return
	}

	key = registry.CatalogKey{
		Namespace: r.catsrc.GetNamespace(),
		Name:      r.catsrc.GetName(),
	}

	return
}

// RegistryProvider aggregates several `CatalogSources` and establishes gRPC connections to their registry servers.
type RegistryProvider struct {
	queueinformer.Operator
	runOnce sync.Once

	globalNamespace string
	sources         *registrygrpc.SourceStore
	cache           cache.Indexer
	pkgLister       pkglisters.PackageManifestLister
	catsrcLister    operatorslisters.CatalogSourceLister
}

var _ PackageManifestProvider = &RegistryProvider{}

func NewRegistryProvider(ctx context.Context, crClient versioned.Interface, operator queueinformer.Operator, wakeupInterval time.Duration, globalNamespace string) (*RegistryProvider, error) {
	p := &RegistryProvider{
		Operator: operator,

		globalNamespace: globalNamespace,
		cache: cache.NewIndexer(PackageManifestKeyFunc, cache.Indexers{
			cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
			catalogIndex:         catalogIndexFunc,
		}),
	}
	p.sources = registrygrpc.NewSourceStore(logrus.New(), stateTimeout, readyTimeout, p.syncSourceState)
	p.pkgLister = pkglisters.NewPackageManifestLister(p.cache)

	// Register queue and QueueInformer
	informerFactory := externalversions.NewSharedInformerFactoryWithOptions(crClient, wakeupInterval, externalversions.WithNamespace(metav1.NamespaceAll))
	catsrcInformer := informerFactory.Operators().V1alpha1().CatalogSources()
	catsrcQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithInformer(catsrcInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(p.syncCatalogSource).ToSyncerWithDelete(p.catalogSourceDeleted)),
	)
	if err != nil {
		return nil, err
	}
	if err := p.RegisterQueueInformer(catsrcQueueInformer); err != nil {
		return nil, err
	}
	p.catsrcLister = catsrcInformer.Lister()

	return p, nil
}

// Run starts the provider's source connection management and catalog informers without blocking.
func (p *RegistryProvider) Run(ctx context.Context) {
	p.runOnce.Do(func() {
		// Both are non-blocking
		p.sources.Start(ctx)
		p.Operator.Run(ctx)
	})
}

func (p *RegistryProvider) syncCatalogSource(obj interface{}) (syncError error) {
	source, ok := obj.(*operatorsv1alpha1.CatalogSource)
	if !ok {
		logrus.Errorf("catalogsource type assertion failed: wrong type: %#v", obj)
	}

	logger := logrus.WithFields(logrus.Fields{
		"action":    "sync catalogsource",
		"name":      source.GetName(),
		"namespace": source.GetNamespace(),
	})

	if source.Status.RegistryServiceStatus == nil {
		logger.Debug("registry service is not ready for grpc connection")
		return
	}

	address := source.Address()
	logger = logger.WithField("address", address)

	key := registry.CatalogKey{
		Namespace: source.GetNamespace(),
		Name:      source.GetName(),
	}

	if sourceMeta := p.sources.GetMeta(key); sourceMeta != nil && sourceMeta.Address == address {
		logger.Infof("updating PackageManifest based on CatalogSource changes: %v", key)
		timeout, cancel := context.WithTimeout(context.Background(), cacheTimeout)
		defer cancel()
		var client *registryClient
		client, syncError = p.registryClient(key)
		if syncError != nil {
			return
		}
		syncError = p.refreshCache(timeout, client)
		return
	}

	logger.Info("connecting to source")
	if _, syncError = p.sources.Add(key, address); syncError != nil {
		logger.Warn("failed to create a new source")
	}

	return
}

func (p *RegistryProvider) syncSourceState(state registrygrpc.SourceState) {
	key := state.Key
	logger := logrus.WithFields(logrus.Fields{
		"action": "sync source",
		"source": key,
		"state":  state.State,
	})
	logger.Debug("source state changed")

	timeout, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()

	var err error
	switch state.State {
	case connectivity.Ready:
		var client *registryClient
		client, err = p.registryClient(key)
		if err == nil {
			err = p.refreshCache(timeout, client)
		}
	case connectivity.TransientFailure, connectivity.Shutdown:
		err = p.gcPackages(key, nil)
	default:
		logger.Debug("inert source state, skipping cache update")
	}

	if err != nil {
		logger.WithError(err).Warn("failed to update cache")
	}
}

func (p *RegistryProvider) registryClient(key registry.CatalogKey) (client *registryClient, err error) {
	source := p.sources.Get(key)
	if source == nil {
		err = fmt.Errorf("missing source for catalog %s", key)
		return
	}

	conn := source.Conn
	if conn == nil {
		err = fmt.Errorf("missing grpc connection for source %s", key)
		return
	}

	var catsrc *operatorsv1alpha1.CatalogSource
	catsrc, err = p.catsrcLister.CatalogSources(key.Namespace).Get(key.Name)
	if err != nil {
		return
	}

	client = newRegistryClient(catsrc, conn)
	return
}

func (p *RegistryProvider) refreshCache(ctx context.Context, client *registryClient) error {
	key, err := client.key()
	if err != nil {
		return err
	}

	logger := logrus.WithFields(logrus.Fields{
		"action": "refresh cache",
		"source": key,
	})

	stream, err := client.ListPackages(ctx, &api.ListPackageRequest{})
	if err != nil {
		logger.WithField("err", err.Error()).Warnf("error getting stream")
		return nil
	}

	var (
		added = map[string]struct{}{}
		mu    sync.Mutex
		wg    sync.WaitGroup
	)
	for {
		pkgName, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.WithField("err", err.Error()).Warnf("error getting data")
			break
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			pkg, err := client.GetPackage(ctx, &api.GetPackageRequest{Name: pkgName.GetName()})
			if err != nil {
				logger.WithField("err", err.Error()).Warnf("eliding package: error getting package")
				return
			}

			newPkg, err := newPackageManifest(ctx, logger, pkg, client)
			if err != nil {
				logger.WithField("err", err.Error()).Warnf("eliding package: error converting to packagemanifest")
				return
			}

			if err := p.cache.Add(newPkg); err != nil {
				logger.WithField("err", err.Error()).Warnf("eliding package: failed to add to cache")
				return
			}

			mu.Lock()
			defer mu.Unlock()
			added[newPkg.GetName()] = struct{}{}
		}()
	}

	logger.Debug("caching new packages...")
	wg.Wait()
	logger.Debug("new packages cached")

	// Garbage collect orphaned packagemanifests from the cache
	return p.gcPackages(key, added)
}

func (p *RegistryProvider) gcPackages(key registry.CatalogKey, keep map[string]struct{}) error {
	logger := logrus.WithFields(logrus.Fields{
		"action": "gc cache",
		"source": key.String(),
	})

	storedPkgKeys, err := p.cache.IndexKeys(catalogIndex, key.String())
	if err != nil {
		return err
	}

	var errs []error
	for _, storedPkgKey := range storedPkgKeys {
		_, _, name, _ := SplitPackageManifestKey(storedPkgKey)
		if keep != nil {
			if _, ok := keep[name]; ok {
				continue
			}
		}
		if err := p.cache.Delete(string(storedPkgKey)); err != nil {
			logger.WithField("pkg", name).WithError(err).Warn("failed to delete cache entry")
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (p *RegistryProvider) catalogSourceDeleted(obj interface{}) {
	catsrc, ok := obj.(metav1.Object)
	if !ok {
		if !ok {
			tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
			if !ok {
				utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
				return
			}

			catsrc, ok = tombstone.Obj.(metav1.Object)
			if !ok {
				utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a Namespace %#v", obj))
				return
			}
		}
	}

	key := registry.CatalogKey{
		Namespace: catsrc.GetNamespace(),
		Name:      catsrc.GetName(),
	}
	logger := logrus.WithFields(logrus.Fields{
		"action": "CatalogSource Deleted",
		"source": key.String(),
	})

	if err := p.sources.Remove(key); err != nil {
		logger.WithError(err).Warn("failed to remove source")
	}

	if err := p.gcPackages(key, nil); err != nil {
		logger.WithError(err).Warn("failed to gc orphaned packages in cache")
	}
}

func (p *RegistryProvider) Get(namespace, name string) (*operators.PackageManifest, error) {
	logger := logrus.WithFields(logrus.Fields{
		"action":    "Get PackageManifest",
		"name":      name,
		"namespace": namespace,
	})

	pkgs, err := p.List(namespace, labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("could not list packages in namespace %s", namespace)
	}

	for _, pkg := range pkgs.Items {
		if pkg.GetName() == name {
			return &pkg, nil
		}
	}

	logger.Info("package not found")
	return nil, nil
}

func (p *RegistryProvider) List(namespace string, selector labels.Selector) (*operators.PackageManifestList, error) {
	var pkgs []*operators.PackageManifest
	if namespace == metav1.NamespaceAll {
		all, err := p.pkgLister.List(selector)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, all...)
	} else {
		nsPkgs, err := p.pkgLister.PackageManifests(namespace).List(selector)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, nsPkgs...)

		if namespace != p.globalNamespace {
			globalPkgs, err := p.pkgLister.PackageManifests(p.globalNamespace).List(selector)
			if err != nil {
				return nil, err
			}

			pkgs = append(pkgs, globalPkgs...)
		}
	}

	pkgList := &operators.PackageManifestList{}
	for _, pkg := range pkgs {
		out := pkg.DeepCopy()
		// Set request namespace to stop k8s clients from complaining about namespace mismatch.
		if namespace != metav1.NamespaceAll {
			out.SetNamespace(namespace)
		}
		pkgList.Items = append(pkgList.Items, *out)
	}

	return pkgList, nil
}

func newPackageManifest(ctx context.Context, logger *logrus.Entry, pkg *api.Package, client *registryClient) (*operators.PackageManifest, error) {
	pkgChannels := pkg.GetChannels()
	catsrc := client.catsrc
	manifest := &operators.PackageManifest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pkg.GetName(),
			Namespace: catsrc.GetNamespace(),
			Labels: utillabels.CloneAndAddLabel(
				utillabels.CloneAndAddLabel(catsrc.GetLabels(),
					"catalog", catsrc.GetName()), "catalog-namespace", catsrc.GetNamespace()),
			CreationTimestamp: catsrc.GetCreationTimestamp(),
		},
		Status: operators.PackageManifestStatus{
			CatalogSource:            catsrc.GetName(),
			CatalogSourceDisplayName: catsrc.Spec.DisplayName,
			CatalogSourcePublisher:   catsrc.Spec.Publisher,
			CatalogSourceNamespace:   catsrc.GetNamespace(),
			PackageName:              pkg.Name,
			DefaultChannel:           pkg.GetDefaultChannelName(),
		},
	}

	var (
		providerSet   bool
		defaultElided bool
		defaultCsv    *operatorsv1alpha1.ClusterServiceVersion
	)
	for _, pkgChannel := range pkgChannels {
		bundle, err := client.GetBundleForChannel(ctx, &api.GetBundleInChannelRequest{PkgName: pkg.GetName(), ChannelName: pkgChannel.GetName()})
		if err != nil {
			logger.WithError(err).WithField("channel", pkgChannel.GetName()).Warn("error getting bundle, eliding channel")
			defaultElided = defaultElided || pkgChannel.Name == manifest.Status.DefaultChannel
			continue
		}

		csv := operatorsv1alpha1.ClusterServiceVersion{}
		err = json.Unmarshal([]byte(bundle.GetCsvJson()), &csv)
		if err != nil {
			logger.WithError(err).WithField("channel", pkgChannel.GetName()).Warn("error unmarshaling csv, eliding channel")
			defaultElided = defaultElided || pkgChannel.Name == manifest.Status.DefaultChannel
			continue
		}
		if defaultCsv == nil || pkgChannel.GetName() == manifest.Status.DefaultChannel {
			defaultCsv = &csv
		}
		manifest.Status.Channels = append(manifest.Status.Channels, operators.PackageChannel{
			Name:           pkgChannel.GetName(),
			CurrentCSV:     csv.GetName(),
			CurrentCSVDesc: operators.CreateCSVDescription(&csv, bundle.GetCsvJson()),
		})

		if manifest.Status.DefaultChannel != "" && pkgChannel.GetName() == manifest.Status.DefaultChannel || !providerSet {
			manifest.Status.Provider = operators.AppLink{
				Name: csv.Spec.Provider.Name,
				URL:  csv.Spec.Provider.URL,
			}
			manifest.ObjectMeta.Labels["provider"] = manifest.Status.Provider.Name
			manifest.ObjectMeta.Labels["provider-url"] = manifest.Status.Provider.URL
			providerSet = true
		}
	}

	if len(manifest.Status.Channels) == 0 {
		return nil, fmt.Errorf("packagemanifest has no valid channels")
	}

	if defaultElided {
		logger.Warn("default channel elided, setting as first in packagemanifest")
		manifest.Status.DefaultChannel = manifest.Status.Channels[0].Name
	}
	manifestLabels := manifest.GetLabels()
	for k, v := range defaultCsv.GetLabels() {
		manifestLabels[k] = v
	}
	setDefaultOsArchLabels(manifestLabels)
	manifest.SetLabels(manifestLabels)
	return manifest, nil
}
