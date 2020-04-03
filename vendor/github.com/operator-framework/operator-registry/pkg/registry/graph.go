package registry

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
