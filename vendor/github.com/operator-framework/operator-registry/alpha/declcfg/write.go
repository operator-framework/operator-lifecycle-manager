package declcfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/operator-framework/operator-registry/alpha/property"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

type MermaidWriter struct {
	MinEdgeName          string
	SpecifiedPackageName string
}

type MermaidOption func(*MermaidWriter)

func NewMermaidWriter(opts ...MermaidOption) *MermaidWriter {
	const (
		minEdgeName          = ""
		specifiedPackageName = ""
	)
	m := &MermaidWriter{
		MinEdgeName:          minEdgeName,
		SpecifiedPackageName: specifiedPackageName,
	}

	for _, opt := range opts {
		opt(m)
	}
	return m
}

func WithMinEdgeName(minEdgeName string) MermaidOption {
	return func(o *MermaidWriter) {
		o.MinEdgeName = minEdgeName
	}
}

func WithSpecifiedPackageName(specifiedPackageName string) MermaidOption {
	return func(o *MermaidWriter) {
		o.SpecifiedPackageName = specifiedPackageName
	}
}

// writes out the channel edges of the declarative config graph in a mermaid format capable of being pasted into
// mermaid renderers like github, mermaid.live, etc.
// output is sorted lexicographically by package name, and then by channel name
// if provided, minEdgeName will be used as the lower bound for edges in the output graph
//
// Example output:
// graph LR
//
//	  %% package "neuvector-certified-operator-rhmp"
//	  subgraph "neuvector-certified-operator-rhmp"
//	     %% channel "beta"
//	     subgraph neuvector-certified-operator-rhmp-beta["beta"]
//		      neuvector-certified-operator-rhmp-beta-neuvector-operator.v1.2.8["neuvector-operator.v1.2.8"]
//		      neuvector-certified-operator-rhmp-beta-neuvector-operator.v1.2.9["neuvector-operator.v1.2.9"]
//		      neuvector-certified-operator-rhmp-beta-neuvector-operator.v1.3.0["neuvector-operator.v1.3.0"]
//		      neuvector-certified-operator-rhmp-beta-neuvector-operator.v1.3.0["neuvector-operator.v1.3.0"]-- replaces --> neuvector-certified-operator-rhmp-beta-neuvector-operator.v1.2.8["neuvector-operator.v1.2.8"]
//		      neuvector-certified-operator-rhmp-beta-neuvector-operator.v1.3.0["neuvector-operator.v1.3.0"]-- skips --> neuvector-certified-operator-rhmp-beta-neuvector-operator.v1.2.9["neuvector-operator.v1.2.9"]
//	    end
//	  end
//
// end
func (writer *MermaidWriter) WriteChannels(cfg DeclarativeConfig, out io.Writer) error {
	pkgs := map[string]*strings.Builder{}

	sort.Slice(cfg.Channels, func(i, j int) bool {
		return cfg.Channels[i].Name < cfg.Channels[j].Name
	})

	versionMap, err := getBundleVersions(&cfg)
	if err != nil {
		return err
	}

	// establish a 'floor' version, either specified by user or entirely open
	minVersion := semver.Version{Major: 0, Minor: 0, Patch: 0}

	if writer.MinEdgeName != "" {
		if _, ok := versionMap[writer.MinEdgeName]; !ok {
			return fmt.Errorf("unknown minimum edge name: %q", writer.MinEdgeName)
		}
		minVersion = versionMap[writer.MinEdgeName]
	}

	// build increasing-version-ordered bundle names, so we can meaningfully iterate over a range
	orderedBundles := []string{}
	for n, _ := range versionMap {
		orderedBundles = append(orderedBundles, n)
	}
	sort.Slice(orderedBundles, func(i, j int) bool {
		return versionMap[orderedBundles[i]].LT(versionMap[orderedBundles[j]])
	})

	minEdgePackage := writer.getMinEdgePackage(&cfg)

	for _, c := range cfg.Channels {
		filteredChannel := writer.filterChannel(&c, versionMap, minVersion, minEdgePackage)
		if filteredChannel != nil {
			pkgBuilder, ok := pkgs[c.Package]
			if !ok {
				pkgBuilder = &strings.Builder{}
				pkgs[c.Package] = pkgBuilder
			}

			channelID := fmt.Sprintf("%s-%s", filteredChannel.Package, filteredChannel.Name)
			pkgBuilder.WriteString(fmt.Sprintf("    %%%% channel %q\n", filteredChannel.Name))
			pkgBuilder.WriteString(fmt.Sprintf("    subgraph %s[%q]\n", channelID, filteredChannel.Name))

			for _, ce := range filteredChannel.Entries {
				if versionMap[ce.Name].GE(minVersion) {
					entryId := fmt.Sprintf("%s-%s", channelID, ce.Name)
					pkgBuilder.WriteString(fmt.Sprintf("      %s[%q]\n", entryId, ce.Name))

					if len(ce.Replaces) > 0 {
						replacesId := fmt.Sprintf("%s-%s", channelID, ce.Replaces)
						pkgBuilder.WriteString(fmt.Sprintf("      %s[%q]-- %s --> %s[%q]\n", entryId, ce.Name, "replaces", replacesId, ce.Replaces))
					}
					if len(ce.Skips) > 0 {
						for _, s := range ce.Skips {
							skipsId := fmt.Sprintf("%s-%s", channelID, s)
							pkgBuilder.WriteString(fmt.Sprintf("      %s[%q]-- %s --> %s[%q]\n", entryId, ce.Name, "skips", skipsId, s))
						}
					}
					if len(ce.SkipRange) > 0 {
						skipRange, err := semver.ParseRange(ce.SkipRange)
						if err == nil {
							for _, edgeName := range filteredChannel.Entries {
								if skipRange(versionMap[edgeName.Name]) {
									skipRangeId := fmt.Sprintf("%s-%s", channelID, edgeName.Name)
									pkgBuilder.WriteString(fmt.Sprintf("      %s[%q]-- \"%s(%s)\" --> %s[%q]\n", entryId, ce.Name, "skipRange", ce.SkipRange, skipRangeId, edgeName.Name))
								}
							}
						} else {
							fmt.Fprintf(os.Stderr, "warning: ignoring invalid SkipRange for package/edge %q/%q: %v\n", c.Package, ce.Name, err)
						}
					}
				}
			}
			pkgBuilder.WriteString("    end\n")
		}
	}

	out.Write([]byte("graph LR\n"))
	pkgNames := []string{}
	for pname, _ := range pkgs {
		pkgNames = append(pkgNames, pname)
	}
	sort.Slice(pkgNames, func(i, j int) bool {
		return pkgNames[i] < pkgNames[j]
	})
	for _, pkgName := range pkgNames {
		out.Write([]byte(fmt.Sprintf("  %%%% package %q\n", pkgName)))
		out.Write([]byte(fmt.Sprintf("  subgraph %q\n", pkgName)))
		out.Write([]byte(pkgs[pkgName].String()))
		out.Write([]byte("  end\n"))
	}

	return nil
}

// filters the channel edges to include only those which are greater-than-or-equal to the edge named by startVersion
// returns a nil channel if all edges are filtered out
func (writer *MermaidWriter) filterChannel(c *Channel, versionMap map[string]semver.Version, minVersion semver.Version, minEdgePackage string) *Channel {
	// short-circuit if no active filters
	if writer.MinEdgeName == "" && writer.SpecifiedPackageName == "" {
		return c
	}

	// short-circuit if channel's package doesn't match filter
	if writer.SpecifiedPackageName != "" && c.Package != writer.SpecifiedPackageName {
		return nil
	}

	// short-circuit if channel package is mismatch from filter
	if minEdgePackage != "" && c.Package != minEdgePackage {
		return nil
	}

	out := &Channel{Name: c.Name, Package: c.Package, Properties: c.Properties, Entries: []ChannelEntry{}}
	for _, ce := range c.Entries {
		filteredCe := ChannelEntry{Name: ce.Name}
		if writer.MinEdgeName == "" {
			// no minimum-edge specified
			filteredCe.SkipRange = ce.SkipRange
			filteredCe.Replaces = ce.Replaces
			filteredCe.Skips = append(filteredCe.Skips, ce.Skips...)

			// accumulate IFF there are any relevant skips/skipRange/replaces remaining or there never were any to begin with
			// for the case where all skip/skipRange/replaces are retained, this is effectively the original edge with validated linkages
			if len(filteredCe.Replaces) > 0 || len(filteredCe.Skips) > 0 || len(filteredCe.SkipRange) > 0 {
				out.Entries = append(out.Entries, filteredCe)
			} else {
				if len(ce.Replaces) == 0 && len(ce.SkipRange) == 0 && len(ce.Skips) == 0 {
					out.Entries = append(out.Entries, filteredCe)
				}
			}
		} else {
			if ce.Name == writer.MinEdgeName {
				// edge is the 'floor', meaning that since all references are "backward references", and we don't want any references from this edge
				// accumulate w/o references
				out.Entries = append(out.Entries, filteredCe)
			} else {
				// edge needs to be filtered to determine if it is below the floor (bad) or on/above (good)
				if len(ce.Replaces) > 0 && versionMap[ce.Replaces].GTE(minVersion) {
					filteredCe.Replaces = ce.Replaces
				}
				if len(ce.Skips) > 0 {
					filteredSkips := []string{}
					for _, s := range ce.Skips {
						if versionMap[s].GTE(minVersion) {
							filteredSkips = append(filteredSkips, s)
						}
					}
					if len(filteredSkips) > 0 {
						filteredCe.Skips = filteredSkips
					}
				}
				if len(ce.SkipRange) > 0 {
					skipRange, err := semver.ParseRange(ce.SkipRange)
					// if skipRange can't be parsed, just don't filter based on it
					if err == nil && skipRange(minVersion) {
						// specified range includes our floor
						filteredCe.SkipRange = ce.SkipRange
					}
				}
				// accumulate IFF there are any relevant skips/skipRange/replaces remaining, or there never were any to begin with (NOP)
				// but the edge name satisfies the minimum-edge constraint
				// for the case where all skip/skipRange/replaces are retained, this is effectively `ce` but with validated linkages
				if len(filteredCe.Replaces) > 0 || len(filteredCe.Skips) > 0 || len(filteredCe.SkipRange) > 0 {
					out.Entries = append(out.Entries, filteredCe)
				} else {
					if len(ce.Replaces) == 0 && len(ce.SkipRange) == 0 && len(ce.Skips) == 0 && versionMap[filteredCe.Name].GTE(minVersion) {
						out.Entries = append(out.Entries, filteredCe)
					}
				}
			}
		}
	}

	if len(out.Entries) > 0 {
		return out
	} else {
		return nil
	}
}

func parseVersionProperty(b *Bundle) (*semver.Version, error) {
	props, err := property.Parse(b.Properties)
	if err != nil {
		return nil, fmt.Errorf("parse properties for bundle %q: %v", b.Name, err)
	}
	if len(props.Packages) != 1 {
		return nil, fmt.Errorf("bundle %q has multiple %q properties, expected exactly 1", b.Name, property.TypePackage)
	}
	v, err := semver.Parse(props.Packages[0].Version)
	if err != nil {
		return nil, fmt.Errorf("bundle %q has invalid version %q: %v", b.Name, props.Packages[0].Version, err)
	}

	return &v, nil
}

func getBundleVersions(cfg *DeclarativeConfig) (map[string]semver.Version, error) {
	entries := make(map[string]semver.Version)
	for index := range cfg.Bundles {
		if _, ok := entries[cfg.Bundles[index].Name]; !ok {
			ver, err := parseVersionProperty(&cfg.Bundles[index])
			if err != nil {
				return entries, err
			}
			entries[cfg.Bundles[index].Name] = *ver
		}
	}

	return entries, nil
}

func (writer *MermaidWriter) getMinEdgePackage(cfg *DeclarativeConfig) string {
	if writer.MinEdgeName == "" {
		return ""
	}

	for _, c := range cfg.Channels {
		for _, ce := range c.Entries {
			if writer.MinEdgeName == ce.Name {
				return c.Package
			}
		}
	}

	return ""
}

func WriteJSON(cfg DeclarativeConfig, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "    ")
	enc.SetEscapeHTML(false)
	return writeToEncoder(cfg, enc)
}

func WriteYAML(cfg DeclarativeConfig, w io.Writer) error {
	enc := newYAMLEncoder(w)
	enc.SetEscapeHTML(false)
	return writeToEncoder(cfg, enc)
}

type yamlEncoder struct {
	w          io.Writer
	escapeHTML bool
}

func newYAMLEncoder(w io.Writer) *yamlEncoder {
	return &yamlEncoder{w, true}
}

func (e *yamlEncoder) SetEscapeHTML(on bool) {
	e.escapeHTML = on
}

func (e *yamlEncoder) Encode(v interface{}) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(e.escapeHTML)
	if err := enc.Encode(v); err != nil {
		return err
	}
	yamlData, err := yaml.JSONToYAML(buf.Bytes())
	if err != nil {
		return err
	}
	yamlData = append([]byte("---\n"), yamlData...)
	_, err = e.w.Write(yamlData)
	return err
}

type encoder interface {
	Encode(interface{}) error
}

func writeToEncoder(cfg DeclarativeConfig, enc encoder) error {
	pkgNames := sets.NewString()

	packagesByName := map[string][]Package{}
	for _, p := range cfg.Packages {
		pkgName := p.Name
		pkgNames.Insert(pkgName)
		packagesByName[pkgName] = append(packagesByName[pkgName], p)
	}
	channelsByPackage := map[string][]Channel{}
	for _, c := range cfg.Channels {
		pkgName := c.Package
		pkgNames.Insert(pkgName)
		channelsByPackage[pkgName] = append(channelsByPackage[pkgName], c)
	}
	bundlesByPackage := map[string][]Bundle{}
	for _, b := range cfg.Bundles {
		pkgName := b.Package
		pkgNames.Insert(pkgName)
		bundlesByPackage[pkgName] = append(bundlesByPackage[pkgName], b)
	}
	othersByPackage := map[string][]Meta{}
	for _, o := range cfg.Others {
		pkgName := o.Package
		pkgNames.Insert(pkgName)
		othersByPackage[pkgName] = append(othersByPackage[pkgName], o)
	}

	for _, pName := range pkgNames.List() {
		if len(pName) == 0 {
			continue
		}
		pkgs := packagesByName[pName]
		for _, p := range pkgs {
			if err := enc.Encode(p); err != nil {
				return err
			}
		}

		channels := channelsByPackage[pName]
		sort.Slice(channels, func(i, j int) bool {
			return channels[i].Name < channels[j].Name
		})
		for _, c := range channels {
			if err := enc.Encode(c); err != nil {
				return err
			}
		}

		bundles := bundlesByPackage[pName]
		sort.Slice(bundles, func(i, j int) bool {
			return bundles[i].Name < bundles[j].Name
		})
		for _, b := range bundles {
			if err := enc.Encode(b); err != nil {
				return err
			}
		}

		others := othersByPackage[pName]
		sort.SliceStable(others, func(i, j int) bool {
			return others[i].Schema < others[j].Schema
		})
		for _, o := range others {
			if err := enc.Encode(o); err != nil {
				return err
			}
		}
	}

	for _, o := range othersByPackage[""] {
		if err := enc.Encode(o); err != nil {
			return err
		}
	}
	return nil
}
