package image

import (
	"context"
	"errors"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

var _ Registry = &MockRegistry{}

type MockRegistry struct {
	RemoteImages map[Reference]*MockImage
	localImages  map[Reference]*MockImage
	m            sync.RWMutex
}

type MockImage struct {
	Labels map[string]string
	FS     fs.FS
}

func (i *MockImage) unpack(dir string) error {
	return fs.WalkDir(i.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(i.FS, path)
		if err != nil {
			return err
		}
		path = filepath.Join(dir, path)
		pathDir := filepath.Dir(path)
		if err := os.MkdirAll(pathDir, 0777); err != nil {
			return err
		}
		return ioutil.WriteFile(path, data, 0666)
	})
}

func (m *MockRegistry) Pull(_ context.Context, ref Reference) error {
	image, ok := m.RemoteImages[ref]
	if !ok {
		return errors.New("not found")
	}
	m.m.Lock()
	defer m.m.Unlock()
	if m.localImages == nil {
		m.localImages = map[Reference]*MockImage{}
	}
	m.localImages[ref] = image
	return nil
}

func (m *MockRegistry) Unpack(_ context.Context, ref Reference, dir string) error {
	m.m.RLock()
	defer m.m.RUnlock()
	image, ok := m.localImages[ref]
	if !ok {
		return errors.New("not found")
	}
	return image.unpack(dir)
}

func (m *MockRegistry) Labels(_ context.Context, ref Reference) (map[string]string, error) {
	m.m.RLock()
	defer m.m.RUnlock()
	image, ok := m.localImages[ref]
	if !ok {
		return nil, errors.New("not found")
	}
	return image.Labels, nil
}

func (m *MockRegistry) Destroy() error {
	m.m.Lock()
	defer m.m.Unlock()
	m.localImages = nil
	return nil
}
