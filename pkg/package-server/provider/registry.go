package provider

import (
	"sync"
	"fmt"
	"context"
	"io"
	"encoding/json"

	"google.golang.org/grpc"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"github.com/operator-framework/operator-registry/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

type registryConn struct {
	source *operatorsv1alpha1.CatalogSource
	conn *grpc.ClientConn
}

// RegistryProvider aggregates several `CatalogSources` and establishes gRPC connections to their registry servers.
type RegistryProvider struct {
	*queueinformer.Operator
	mu sync.RWMutex
	

	globalNamespace string
	conns []registryConn
}

var _ PackageManifestProvider = &RegistryProvider{}

func NewRegistryProvider(informers []cache.SharedIndexInformer, queueOperator *queueinformer.Operator, globalNS string) *RegistryProvider {
	prov := &RegistryProvider{
		Operator:        queueOperator,
		globalNamespace: globalNS,
		conns:           []registryConn{},
	}

	queue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "catalogsources")
	queueInformers := queueinformer.New(
		queue,
		informers,
		nil,
		&cache.ResourceEventHandlerFuncs{prov.catalogSourceAdded, prov.catalogSourceUpdated, prov.catalogSourceDeleted},
		"catsrc",
		metrics.NewMetricsNil(),
		logrus.New(),
	)
	for _, informer := range queueInformers {
		prov.RegisterQueueInformer(informer)
	}

	return prov
}

func (p *RegistryProvider) catalogSourceAdded(obj interface{}) {
	catsrc, ok := obj.(*operatorsv1alpha1.CatalogSource)
	if !ok {
		logrus.Debugf("casting catalog source failed: wrong type: %#v", obj)
	}

	logger := logrus.WithFields(logrus.Fields{
		"action":    "CatalogSource Added",
		"name":      catsrc.GetName(),
		"namespace": catsrc.GetNamespace(),
	})

	conn, err := grpc.Dial(catsrc.Status.RegistryServiceStatus.Address())
	if err != nil {
		logger.Errorf("could not connect to registry service: %s", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.conns = append(p.conns, registryConn{catsrc, conn})
}

// FIXME(alecmerdler): Registry pod will restart whenever the CatalogSource/ConfigMap are updated, which will kill the gRPC connection
func (p *RegistryProvider) catalogSourceUpdated(obj interface{}, newObj interface{}) {
	catsrc, ok := newObj.(*operatorsv1alpha1.CatalogSource)
	if !ok {
		logrus.Debugf("casting catalog source failed: wrong type: %#v", newObj)
	}

	logger := logrus.WithFields(logrus.Fields{
		"action":    "CatalogSource Updated",
		"name":      catsrc.GetName(),
		"namespace": catsrc.GetNamespace(),
	})
	logger.Errorf("attempting to update gRPC connection")

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.conns {
		if conn.source.GetName() == catsrc.GetName() && conn.source.GetNamespace() == catsrc.GetNamespace() {
			p.mu.Lock()
			defer p.mu.Unlock()

			conn.conn.Close()
			newConn, err := grpc.Dial(catsrc.Status.RegistryServiceStatus.Address())
			if err != nil {
				logger.Errorf("could not connect to registry service: %s", err)
			}
			conn.conn = newConn
			return
		}
	}
	logger.Errorf("gRPC connection not found")
}

func (p *RegistryProvider) catalogSourceDeleted(obj interface{}) {
	catsrc, ok := obj.(*operatorsv1alpha1.CatalogSource)
	if !ok {
		logrus.Debugf("casting catalog source failed: wrong type: %#v", obj)
	}

	logger := logrus.WithFields(logrus.Fields{
		"action":    "CatalogSource Deleted",
		"name":      catsrc.GetName(),
		"namespace": catsrc.GetNamespace(),
	})
	logger.Debugf("attempting to remove gRPC connection")

	p.mu.Lock()
	defer p.mu.Unlock()

	for i, conn := range p.conns {
		if conn.source.GetName() == catsrc.GetName() && conn.source.GetNamespace() == catsrc.GetNamespace() {
			p.mu.Lock()
			defer p.mu.Unlock()

			conn.conn.Close()
			p.conns = append(p.conns[:i], p.conns[i+1:]...)
			logger.Debug("removed gRPC connection")
			return
		}
	}
	logger.Errorf("gRPC connection not found")
}

func (p *RegistryProvider) Get(namespace, name string) (*v1alpha1.PackageManifest, error) {
	logger := logrus.WithFields(logrus.Fields{
		"action":    "Get PackageManifest",
		"name":      name,
		"namespace": namespace,
	})

	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, conn := range p.conns {
		if conn.source.GetName() == name && conn.source.GetNamespace() == namespace || conn.source.GetNamespace() == p.globalNamespace {
			client := api.NewRegistryClient(conn.conn)
			pkg, err := client.GetPackage(context.Background(), &api.GetPackageRequest{name})
			if err != nil {
				return nil, err
			}
			return toPackageManifest(pkg, conn.source, client)
		}
	}
	logger.Errorf("package not found")
	
	return nil, fmt.Errorf("package %s not found in namespace %s", name, namespace)
}

func (p *RegistryProvider) List(namespace string) (*v1alpha1.PackageManifestList, error) {
	logger := logrus.WithFields(logrus.Fields{
		"action":    "List PackageManifests",
		"namespace": namespace,
	})

	p.mu.RLock()
	defer p.mu.RUnlock()

	pkgs := []v1alpha1.PackageManifest{}
	for _, conn := range p.conns {
		if conn.source.GetNamespace() == namespace || conn.source.GetNamespace() == p.globalNamespace || namespace == "" {
			logger.Debugf("found CatalogSource %s", conn.source.GetName())

			client := api.NewRegistryClient(conn.conn)
			stream, err := client.ListPackages(context.Background(), &api.ListPackageRequest{})
			if err != nil {
				return nil, err
			}
			for {
				pkgName, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}
				pkg, err := client.GetPackage(context.Background(), &api.GetPackageRequest{pkgName.GetName()})
				if err != nil {
					return nil, err
				}
				newPkg, err := toPackageManifest(pkg, conn.source, client)
				pkgs = append(pkgs, *newPkg)
			}
		}
	}

	return &v1alpha1.PackageManifestList{Items: pkgs}, nil
}

func (p *RegistryProvider) Subscribe(namespace string, stopCh <-chan struct{}) (PackageChan, PackageChan, PackageChan, error) {
	logger := logrus.WithFields(logrus.Fields{
		"Action":    "PackageManifest Subscribe",
		"namespace": namespace,
	})

	add := make(chan v1alpha1.PackageManifest)
	modify := make(chan v1alpha1.PackageManifest)
	delete := make(chan v1alpha1.PackageManifest)

	p.mu.RLock()
	defer p.mu.RUnlock()
	
	for _, conn := range p.conns {
		if conn.source.GetNamespace() == namespace || conn.source.GetNamespace() == p.globalNamespace || namespace == "" {
			logger.Debugf("found CatalogSource %s", conn.source.GetName())
			// TODO(alecmerdler): Actually implement watching
		}
	}
	// TODO(alecmerdler): Just send on the `stopCh` to kill the watch if any of the registry pods restart
	go func() {
		<-stopCh 
		close(add)
		close(modify)
		close(delete)
		return
	}()

	return add, modify, delete, nil
}

func toPackageManifest(pkg *api.Package, catsrc *operatorsv1alpha1.CatalogSource, client api.RegistryClient) (*v1alpha1.PackageManifest, error) {
	manifest := &v1alpha1.PackageManifest{
		ObjectMeta: metav1.ObjectMeta{
			Name: pkg.GetName(),
			Namespace: catsrc.GetNamespace(),
			Labels: catsrc.GetLabels(),
		},
		Status: v1alpha1.PackageManifestStatus{
			CatalogSource: catsrc.GetName(),
			CatalogSourceDisplayName: catsrc.Spec.DisplayName,
			CatalogSourcePublisher: catsrc.Spec.Publisher,
			CatalogSourceNamespace: catsrc.GetNamespace(),
			PackageName: pkg.Name,
			Channels: []v1alpha1.PackageChannel{},
			DefaultChannel: pkg.GetDefaultChannelName(),
		},
	}
	manifest.ObjectMeta.Labels["catalog"] = manifest.Status.CatalogSource
	manifest.ObjectMeta.Labels["catalog-namespace"] = manifest.Status.CatalogSourceNamespace

	for i, pkgChannel := range pkg.GetChannels() {
		bundle, err := client.GetBundleForChannel(context.Background(), &api.GetBundleInChannelRequest{pkg.GetName(), pkgChannel.GetName()})
		if err != nil {
			return nil, err
		}
		csv := operatorsv1alpha1.ClusterServiceVersion{}
		err = json.Unmarshal([]byte(bundle.GetCsvJson()), &csv)
		if err != nil {
			return nil, err
		}
		manifest.Status.Channels[i] = v1alpha1.PackageChannel{
			Name: pkgChannel.GetName(),
			CurrentCSV: csv.GetName(),
			CurrentCSVDesc: v1alpha1.CreateCSVDescription(&csv),
		}

		if manifest.Status.DefaultChannel != "" && csv.GetName() == manifest.Status.DefaultChannel || i == 0 {
			manifest.Status.Provider = v1alpha1.AppLink{
				Name: csv.Spec.Provider.Name,
				URL:  csv.Spec.Provider.URL,
			}
			manifest.ObjectMeta.Labels["provider"] = manifest.Status.Provider.Name
			manifest.ObjectMeta.Labels["provider-url"] = manifest.Status.Provider.URL
		}
	}

	return manifest, nil
}