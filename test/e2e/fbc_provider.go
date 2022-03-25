package e2e

import (
	"errors"
	"fmt"
	"os"
)

type FileBasedCatalogProvider interface {
	GetCatalog() string
}

type fileBasedFileBasedCatalogProvider struct {
	fbc string
}

func NewFileBasedFiledBasedCatalogProvider(path string) (FileBasedCatalogProvider, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("path %s does not exist: %w", path, err)
	}
	if err != nil {
		return nil, err
	}

	return &fileBasedFileBasedCatalogProvider{
		fbc: string(data),
	}, nil
}

func (f *fileBasedFileBasedCatalogProvider) GetCatalog() string {
	return f.fbc
}
