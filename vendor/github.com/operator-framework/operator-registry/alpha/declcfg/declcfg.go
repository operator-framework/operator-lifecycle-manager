package declcfg

import (
	"encoding/json"

	"github.com/operator-framework/operator-registry/alpha/property"
)

const (
	SchemaPackage = "olm.package"
	SchemaChannel = "olm.channel"
	SchemaBundle  = "olm.bundle"
)

type DeclarativeConfig struct {
	Packages []Package
	Channels []Channel
	Bundles  []Bundle
	Others   []Meta
}

type Package struct {
	Schema         string              `json:"schema"`
	Name           string              `json:"name"`
	DefaultChannel string              `json:"defaultChannel"`
	Icon           *Icon               `json:"icon,omitempty"`
	Description    string              `json:"description,omitempty"`
	Properties     []property.Property `json:"properties,omitempty" hash:"set"`
}

type Icon struct {
	Data      []byte `json:"base64data"`
	MediaType string `json:"mediatype"`
}

type Channel struct {
	Schema     string              `json:"schema"`
	Name       string              `json:"name"`
	Package    string              `json:"package"`
	Entries    []ChannelEntry      `json:"entries"`
	Properties []property.Property `json:"properties,omitempty" hash:"set"`
}

type ChannelEntry struct {
	Name      string   `json:"name"`
	Replaces  string   `json:"replaces,omitempty"`
	Skips     []string `json:"skips,omitempty"`
	SkipRange string   `json:"skipRange,omitempty"`
}

// Bundle specifies all metadata and data of a bundle object.
// Top-level fields are the source of truth, i.e. not CSV values.
//
// Notes:
// - Any field slice type field or type containing a slice somewhere
//   where two types/fields are equal if their contents are equal regardless
//   of order must have a `hash:"set"` field tag for bundle comparison.
// - Any fields that have a `json:"-"` tag must be included in the equality
//   evaluation in bundlesEqual().
type Bundle struct {
	Schema        string              `json:"schema"`
	Name          string              `json:"name"`
	Package       string              `json:"package"`
	Image         string              `json:"image"`
	Properties    []property.Property `json:"properties,omitempty" hash:"set"`
	RelatedImages []RelatedImage      `json:"relatedImages,omitempty" hash:"set"`

	// These fields are present so that we can continue serving
	// the GRPC API the way packageserver expects us to in a
	// backwards-compatible way. These are populated from
	// any `olm.bundle.object` properties.
	//
	// These fields will never be persisted in the bundle blob as
	// first class fields.
	CsvJSON string   `json:"-"`
	Objects []string `json:"-"`
}

type RelatedImage struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

type Meta struct {
	Schema  string
	Package string

	Blob json.RawMessage
}

func (m Meta) MarshalJSON() ([]byte, error) {
	return m.Blob, nil
}

func (m *Meta) UnmarshalJSON(blob []byte) error {
	type tmp struct {
		Schema     string              `json:"schema"`
		Package    string              `json:"package,omitempty"`
		Properties []property.Property `json:"properties,omitempty"`
	}
	var t tmp
	if err := json.Unmarshal(blob, &t); err != nil {
		return err
	}
	m.Schema = t.Schema
	m.Package = t.Package
	m.Blob = blob
	return nil
}
