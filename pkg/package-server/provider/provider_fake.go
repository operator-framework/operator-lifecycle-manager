package provider

import (
	"sync"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
)

var _ PackageManifestProvider = &FakeProvider{}

// FakeProvider is used for testing.
type FakeProvider struct {
	manifests map[packageKey]v1alpha1.PackageManifest
	add       []chan v1alpha1.PackageManifest
	modify    []chan v1alpha1.PackageManifest
	delete    []chan v1alpha1.PackageManifest

	mu sync.Mutex
}

func NewFakeProvider() *FakeProvider {
	return &FakeProvider{
		make(map[packageKey]v1alpha1.PackageManifest),
		[]chan v1alpha1.PackageManifest{},
		[]chan v1alpha1.PackageManifest{},
		[]chan v1alpha1.PackageManifest{},
		sync.Mutex{},
	}
}

func (f *FakeProvider) Get(namespace, name string) (*v1alpha1.PackageManifest, error) {
	return nil, nil
}

func (f *FakeProvider) List(namespace string) (*v1alpha1.PackageManifestList, error) {
	return nil, nil
}

func (f *FakeProvider) Subscribe(stopCh <-chan struct{}) (PackageChan, PackageChan, PackageChan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	add := make(chan v1alpha1.PackageManifest)
	modify := make(chan v1alpha1.PackageManifest)
	delete := make(chan v1alpha1.PackageManifest)
	f.add = append(f.add, add)
	f.modify = append(f.modify, modify)
	f.delete = append(f.delete, delete)

	go func() {
		<-stopCh
		for _, add := range f.add {
			close(add)
		}
		for _, modify := range f.modify {
			close(modify)
		}
		for _, delete := range f.delete {
			close(delete)
		}
	}()

	return add, modify, delete, nil
}

func (f *FakeProvider) Add(manifest v1alpha1.PackageManifest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, add := range f.add {
		add <- manifest
	}
}

func (f *FakeProvider) Modify(manifest v1alpha1.PackageManifest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, modify := range f.modify {
		modify <- manifest
	}
}

func (f *FakeProvider) Delete(manifest v1alpha1.PackageManifest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, delete := range f.delete {
		delete <- manifest
	}
}
