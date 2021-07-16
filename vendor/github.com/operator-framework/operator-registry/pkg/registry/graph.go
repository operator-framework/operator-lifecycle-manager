package registry

import (
	"fmt"
	"strings"
)

type Package struct {
	Name           string
	DefaultChannel string
	Channels       map[string]Channel
}

func (p *Package) String() string {
	var b strings.Builder
	b.WriteString("name: ")
	b.WriteString(p.Name)
	b.WriteString("\ndefault channel: ")
	b.WriteString(p.DefaultChannel)
	b.WriteString("\nchannels:\n")

	for n, c := range p.Channels {
		b.WriteString(n)
		b.WriteString("\n")
		b.WriteString(c.String())
	}

	return b.String()
}

type Channel struct {
	Head  BundleKey
	Nodes map[BundleKey]map[BundleKey]struct{}
}

func (c *Channel) String() string {
	var b strings.Builder
	for node, _ := range c.Nodes {
		b.WriteString(node.String())
		b.WriteString("\n")
	}

	return b.String()
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
