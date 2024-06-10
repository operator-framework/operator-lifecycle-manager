package composite

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/operator-framework/operator-registry/pkg/image"
)

type TemplateDefinition struct {
	Schema string
	Config json.RawMessage
}

type BasicConfig struct {
	Input  string
	Output string
}

type SemverConfig struct {
	Input  string
	Output string
}

type RawConfig struct {
	Input  string
	Output string
}

type CustomConfig struct {
	Command string
	Args    []string
	Output  string
}

type BuilderMap map[string]Builder

type CatalogBuilderMap map[string]BuilderMap

type builderFunc func(BuilderConfig) Builder

type Template struct {
	catalogFile        io.Reader
	contributionFile   io.Reader
	contributionPath   string
	validate           bool
	outputType         string
	registry           image.Registry
	registeredBuilders map[string]builderFunc
}

type TemplateOption func(t *Template)

type Specs struct {
	CatalogSpec      *CatalogConfig
	ContributionSpec *CompositeConfig
}

type HttpGetter interface {
	Get(url string) (*http.Response, error)
}
