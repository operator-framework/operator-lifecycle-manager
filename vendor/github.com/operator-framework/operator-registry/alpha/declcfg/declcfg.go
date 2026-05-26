package declcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"golang.org/x/text/cases"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/operator-framework/operator-registry/alpha/model"
	"github.com/operator-framework/operator-registry/alpha/property"
	prettyunmarshaler "github.com/operator-framework/operator-registry/pkg/prettyunmarshaler"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

// Re-export VersionRelease/Release types/functions from model package to make it possible for users to only include this package and avoid import cycles
type (
	Release        = model.Release
	VersionRelease = model.VersionRelease
)

var NewRelease = model.NewRelease

const (
	SchemaPackage     = "olm.package"
	SchemaChannel     = "olm.channel"
	SchemaBundle      = "olm.bundle"
	SchemaDeprecation = "olm.deprecations"
)

type DeclarativeConfig struct {
	Packages     []Package
	Channels     []Channel
	Bundles      []Bundle
	Deprecations []Deprecation
	Others       []Meta
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
//   - Any field slice type field or type containing a slice somewhere
//     where two types/fields are equal if their contents are equal regardless
//     of order must have a `hash:"set"` field tag for bundle comparison.
//   - Any fields that have a `json:"-"` tag must be included in the equality
//     evaluation in bundlesEqual().
type Bundle struct {
	Schema        string              `json:"schema"`
	Name          string              `json:"name,omitempty"`
	Package       string              `json:"package,omitempty"`
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

type Deprecation struct {
	Schema  string             `json:"schema"`
	Package string             `json:"package"`
	Entries []DeprecationEntry `json:"entries"`
}

type DeprecationEntry struct {
	Reference PackageScopedReference `json:"reference"`
	Message   string                 `json:"message"`
}

type PackageScopedReference struct {
	Schema string `json:"schema"`
	Name   string `json:"name,omitempty"`
}

type Meta struct {
	Schema  string
	Package string
	Name    string

	Blob json.RawMessage
}

func (m Meta) MarshalJSON() ([]byte, error) {
	return m.Blob, nil
}

func (m *Meta) UnmarshalJSON(blob []byte) error {
	blobMap := map[string]interface{}{}
	if err := json.Unmarshal(blob, &blobMap); err != nil {
		// TODO: unfortunately, there are libraries between here and the original caller
		//   that eat our error type and return a generic error, such that we lose the
		//   ability to errors.As to get this error on the other side. For now, just return
		//   a string error that includes the pretty printed message.
		return errors.New(prettyunmarshaler.NewJSONUnmarshalError(blob, err).Pretty())
	}

	// TODO: this function ensures we do not break backwards compatibility with
	//    the documented examples of FBC templates, which use upper camel case
	//    for JSON field names. We need to decide if we want to continue supporting
	//    case insensitive JSON field names, or if we want to enforce a specific
	//    case-sensitive key value for each field.
	if err := extractUniqueMetaKeys(blobMap, m); err != nil {
		return err
	}

	buf := bytes.Buffer{}
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(blobMap); err != nil {
		return err
	}
	m.Blob = buf.Bytes()
	return nil
}

// extractUniqueMetaKeys enables a case-insensitive key lookup for the schema, package, and name
// fields of the Meta struct. If the blobMap contains duplicate keys (that is, keys have the same folded value),
// an error is returned.
func extractUniqueMetaKeys(blobMap map[string]any, m *Meta) error {
	keySets := map[string]sets.Set[string]{}
	folder := cases.Fold()
	for key := range blobMap {
		foldKey := folder.String(key)
		if _, ok := keySets[foldKey]; !ok {
			keySets[foldKey] = sets.New[string]()
		}
		keySets[foldKey].Insert(key)
	}

	dupErrs := []error{}
	for foldedKey, keys := range keySets {
		if len(keys) != 1 {
			dupErrs = append(dupErrs, fmt.Errorf("duplicate keys for key %q: %v", foldedKey, sets.List(keys)))
		}
	}
	if len(dupErrs) > 0 {
		return utilerrors.NewAggregate(dupErrs)
	}

	metaMap := map[string]*string{
		folder.String("schema"):  &m.Schema,
		folder.String("package"): &m.Package,
		folder.String("name"):    &m.Name,
	}

	for foldedKey, ptr := range metaMap {
		// if the folded key doesn't exist in the key set derived from the blobMap, that means
		// the key doesn't exist in the blobMap, so we can skip it
		if _, ok := keySets[foldedKey]; !ok {
			continue
		}

		// reset key to the unfolded key, which we know is the one that appears in the blobMap
		key := keySets[foldedKey].UnsortedList()[0]
		if _, ok := blobMap[key]; !ok {
			continue
		}
		v, ok := blobMap[key].(string)
		if !ok {
			return fmt.Errorf("expected value for key %q to be a string, got %t: %v", key, blobMap[key], blobMap[key])
		}
		*ptr = v
	}
	return nil
}

func (destination *DeclarativeConfig) Merge(src *DeclarativeConfig) {
	destination.Packages = append(destination.Packages, src.Packages...)
	destination.Channels = append(destination.Channels, src.Channels...)
	destination.Bundles = append(destination.Bundles, src.Bundles...)
	destination.Others = append(destination.Others, src.Others...)
	destination.Deprecations = append(destination.Deprecations, src.Deprecations...)
}

// usesLegacyReleaseVersion returns true if the bundle's CSV contains an olm.substitutesFor annotation.
// It checks three possible sources in order:
// 1. CsvJSON field
// 2. olm.csv.metadata property
// 3. olm.bundle.object property containing a CSV
// Returns false if no substitutesFor annotation is found.
// NB: this can only return true for registry+v1 bundles which always have a CSV
func (b *Bundle) usesLegacyReleaseVersion() bool {
	const substitutesForAnnotationKey = "olm.substitutesFor"

	// Path 1: Check CsvJSON field if present
	if b.CsvJSON != "" {
		var csv registry.ClusterServiceVersion
		if err := json.Unmarshal([]byte(b.CsvJSON), &csv); err == nil {
			return csv.GetSubstitutesFor() != ""
		}
		// On error, fall through to check other sources
	}

	// Path 2 & 3: Check properties
	for _, prop := range b.Properties {
		switch prop.Type {
		case property.TypeCSVMetadata:
			var csvMeta property.CSVMetadata
			if err := json.Unmarshal(prop.Value, &csvMeta); err != nil {
				continue
			}
			if csvMeta.Annotations != nil {
				if substitutes, ok := csvMeta.Annotations[substitutesForAnnotationKey]; ok && substitutes != "" {
					return true
				}
			}

		case property.TypeBundleObject:
			var bundleObj property.BundleObject
			if err := json.Unmarshal(prop.Value, &bundleObj); err != nil {
				continue
			}
			var csv registry.ClusterServiceVersion
			if err := json.Unmarshal(bundleObj.Data, &csv); err == nil {
				return csv.GetSubstitutesFor() != ""
			}
		}
	}

	return false
}

// order by version, then
// release, if present
func (b *Bundle) Compare(other *Bundle) int {
	if b.Name == other.Name {
		return 0
	}
	avr, err := b.VersionRelease()
	if err != nil {
		return 0
	}
	otherVr, err := other.VersionRelease()
	if err != nil {
		return 0
	}
	return avr.Compare(otherVr)
}

// constructs a VersionRelease from the olm.package property of the bundle
// this handles the cases where the property is present, missing, or duplicated
// if a release field is present in the property, it is used as-is
// if it is NOT present in the property, but the version field contains build metadata,
// we attempt to convert the build metadata into a release and strip the build metadata from the version.
// This is to support bundles that use the legacy approach of encoding release information in the build metadata field of the version
func (b *Bundle) VersionRelease() (*VersionRelease, error) {
	var (
		vr *VersionRelease
	)
	// loop over all properties, and do not break if we find a package property, in order to check for duplicates
	for _, prop := range b.Properties {
		switch prop.Type {
		case property.TypePackage:
			var p property.Package

			// if we encounter more than one olm.package property, return an error
			if vr != nil {
				return nil, fmt.Errorf("must be exactly one property of type %q", SchemaPackage)
			}

			if err := json.Unmarshal(prop.Value, &p); err != nil {
				return nil, fmt.Errorf("unable to unmarshal \"olm.package\" property for bundle %q: %v", b.Name, err)
			}
			pv, err := semver.Parse(p.Version)
			if err != nil {
				return nil, fmt.Errorf("invalid semver version %q in \"olm.package\" property for bundle %q: %v", p.Version, b.Name, err)
			}
			pr, err := NewRelease(p.Release)
			if err != nil {
				return nil, fmt.Errorf("invalid release %q in \"olm.package\" property for bundle %q: %v", p.Release, b.Name, err)
			}
			vr = &VersionRelease{
				Version: pv,
				Release: pr,
			}
		}
	}
	if vr == nil {
		return nil, fmt.Errorf("no \"olm.package\" property found for bundle %q", b.Name)
	}

	// if the bundle's release isn't provided, see if we can use the legacy build metadata release approach to identify a release
	// if successful, remove the build metadata from the version. Only attempt for bundles using legacy release versioning.
	if len(vr.Release) == 0 && vr.Version.Build != nil && b.usesLegacyReleaseVersion() {
		newrel, err := NewRelease(strings.Join(vr.Version.Build, "."))
		if err != nil {
			return nil, fmt.Errorf("unable to convert build metadata to release for bundle %q: %v", b.Name, err)
		}
		vr.Release = newrel
		vr.Version.Build = nil
	}

	return vr, nil
}
