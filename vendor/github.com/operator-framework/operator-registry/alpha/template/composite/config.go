package composite

const (
	CompositeSchema = "olm.composite"
	CatalogSchema   = "olm.composite.catalogs"
)

type CompositeConfig struct {
	Schema     string
	Components []Component
}

type Component struct {
	Name        string
	Destination ComponentDestination
	Strategy    BuildStrategy
}

type ComponentDestination struct {
	Path string
}

type BuildStrategy struct {
	Name     string
	Template TemplateDefinition
}

type CatalogConfig struct {
	Schema   string
	Catalogs []Catalog
}

type Catalog struct {
	Name        string
	Destination CatalogDestination
	Builders    []string
}

type CatalogDestination struct {
	// BaseImage  string
	WorkingDir string
}
