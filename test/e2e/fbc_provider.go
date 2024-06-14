package e2e

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type FileBasedCatalogProvider interface {
	GetCatalog() string
}

type FileBasedCatalogProviderOption func(provider *fileBasedFileBasedCatalogProvider)

type fileBasedFileBasedCatalogProvider struct {
	fbc string
}

func NewFileBasedFiledBasedCatalogProvider(path string, opts ...FileBasedCatalogProviderOption) (FileBasedCatalogProvider, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("path %s does not exist: %w", path, err)
	}
	if err != nil {
		return nil, err
	}

	p := &fileBasedFileBasedCatalogProvider{
		fbc: string(data),
	}

	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func (f *fileBasedFileBasedCatalogProvider) Modify(mod func(fbc string) string) {
	f.fbc = mod(f.fbc)
}

func (f *fileBasedFileBasedCatalogProvider) GetCatalog() string {
	return f.fbc
}

func NewRawFileBasedCatalogProvider(data string) (FileBasedCatalogProvider, error) {
	return &fileBasedFileBasedCatalogProvider{
		fbc: string(data),
	}, nil
}

func BundleRegistry(registry string) FileBasedCatalogProviderOption {
	return func(provider *fileBasedFileBasedCatalogProvider) {
		provider.fbc = strings.ReplaceAll(provider.fbc, "quay.io/olmtest", registry)
	}
}
