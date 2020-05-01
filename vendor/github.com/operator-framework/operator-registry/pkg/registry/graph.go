package registry

import (
	"fmt"
)

type Package struct {
	Name           string
	DefaultChannel string
	Channels       map[string]Channel
}

type Channel struct {
	Head  BundleKey
	Nodes map[BundleKey]map[BundleKey]struct{}
}

type BundleKey struct {
	BundlePath string
	Version    string //semver string
	CsvName    string
}

func (b *BundleKey) IsEmpty() bool {
	return b.BundlePath == "" && b.Version == "" && b.CsvName == ""
}

func (b *BundleKey) String() string {
	return fmt.Sprintf("%s %s %s", b.CsvName, b.Version, b.BundlePath)
}

func (p *Package) HasChannel(channel string) bool {
	if p.Channels == nil {
		return false
	}

	_, found := p.Channels[channel]
	return found
}

func (p *Package) HasCsv(csv string) bool {
	for _, channelGraph := range p.Channels {
		for node := range channelGraph.Nodes {
			if node.CsvName == csv {
				return true
			}
		}
	}

	return false
}
