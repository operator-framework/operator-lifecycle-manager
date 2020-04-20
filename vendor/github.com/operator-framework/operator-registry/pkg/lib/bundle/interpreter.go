package bundle

import (
	"fmt"
	"path"
	"sort"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

type bundleDirInterpreter struct {
	bundleCsvName string
	pkg           *registry.Package
}

func NewBundleDirInterperter(bundleDir string) (*bundleDirInterpreter, error) {
	csv, err := registry.ReadCSVFromBundleDirectory(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("error loading CSV from bundle directory, %v", err)
	}

	pkgDir, err := registry.NewPackageGraphLoaderFromDir(path.Join(bundleDir, ".."))
	if err != nil {
		return nil, fmt.Errorf("error loading package from directory, %v", err)
	}

	p, err := pkgDir.Generate()
	if err != nil {
		return nil, err
	}

	return &bundleDirInterpreter{bundleCsvName: csv.GetName(), pkg: p}, nil
}

func (b *bundleDirInterpreter) GetBundleChannels() (channelNames []string) {
	for channelName, channel := range b.pkg.Channels {
		for bundle, _ := range channel.Nodes {
			if bundle.CsvName == b.bundleCsvName {
				channelNames = append(channelNames, channelName)
				break
			}
		}
	}
	sort.Strings(channelNames)
	return
}

func (b *bundleDirInterpreter) GetDefaultChannel() string {
	return b.pkg.DefaultChannel
}

func (b *bundleDirInterpreter) GetPackageName() string {
	return b.pkg.Name
}
