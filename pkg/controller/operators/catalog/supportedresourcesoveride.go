package catalog

const (
	DeprecatedKind = "Deprecated"
)

func init() {
	supportedKinds[DeprecatedKind] = struct{}{}
}
