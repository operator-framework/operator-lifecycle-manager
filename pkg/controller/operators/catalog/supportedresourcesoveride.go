//go:build e2e

package catalog

const (
	DeprecatedKind = "Deprecated"
)

func init() {
	supportedKinds[DeprecatedKind] = struct{}{}
}
