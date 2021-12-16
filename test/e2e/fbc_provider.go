package e2e

import (
	"io/ioutil"
)

type FileBasedCatalogProvider interface {
	GetCatalog() string
}

type fileBasedFileBasedCatalogProvider struct {
	fbc string
}

func NewFileBasedFiledBasedCatalogProvider(path string) (FileBasedCatalogProvider, error) {
	data, err := ioutil.ReadFile(path)
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
