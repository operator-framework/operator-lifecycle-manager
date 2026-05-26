package declcfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/operator-framework/operator-registry/alpha/property"
)

type MermaidWriter struct {
	MinEdgeName          string
	SpecifiedPackageName string
	DrawV0Semantics      bool
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
		DrawV0Semantics:      true,
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

func WithV0Semantics(drawV0Semantics bool) MermaidOption {
	return func(o *MermaidWriter) {
		o.DrawV0Semantics = drawV0Semantics
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
	for n := range versionMap {
		orderedBundles = append(orderedBundles, n)
	}
	sort.Slice(orderedBundles, func(i, j int) bool {
		return versionMap[orderedBundles[i]].LT(versionMap[orderedBundles[j]])
	})

	minEdgePackage := writer.getMinEdgePackage(&cfg)

	depByPackage := sets.Set[string]{}
	depByChannel := sets.Set[string]{}
	depByBundle := sets.Set[string]{}

	for _, d := range cfg.Deprecations {
		for _, e := range d.Entries {
			switch e.Reference.Schema {
			case SchemaPackage:
				depByPackage.Insert(d.Package)
			case SchemaChannel:
				depByChannel.Insert(e.Reference.Name)
			case SchemaBundle:
				depByBundle.Insert(e.Reference.Name)
			}
		}
	}

	var deprecatedPackage string
	deprecatedChannelIDs := []string{}
	decoratedBundleIDs := map[string][]string{"deprecated": {}, "skipped": {}, "deprecatedskipped": {}}
	linkID := 0
	skippedLinkIDs := []string{}

	for _, c := range cfg.Channels {
		filteredChannel := writer.filterChannel(&c, versionMap, minVersion, minEdgePackage)
		// nolint:nestif
		if filteredChannel != nil {
			pkgBuilder, ok := pkgs[c.Package]
			if !ok {
				pkgBuilder = &strings.Builder{}
				pkgs[c.Package] = pkgBuilder
			}

			channelID := fmt.Sprintf("%s-%s", filteredChannel.Package, filteredChannel.Name)
			fmt.Fprintf(pkgBuilder, "    %%%% channel %q\n", filteredChannel.Name)
			fmt.Fprintf(pkgBuilder, "    subgraph %s[%q]\n", channelID, filteredChannel.Name)

			if depByPackage.Has(filteredChannel.Package) {
				deprecatedPackage = filteredChannel.Package
			}

			if depByChannel.Has(filteredChannel.Name) {
				deprecatedChannelIDs = append(deprecatedChannelIDs, channelID)
			}

			// sort edges by decreasing version
			sortedEntries := make([]*ChannelEntry, 0, len(filteredChannel.Entries))
			for i := range filteredChannel.Entries {
				sortedEntries = append(sortedEntries, &filteredChannel.Entries[i])
			}
			sort.Slice(sortedEntries, func(i, j int) bool {
				// Sort by decreasing version: greater version comes first
				return versionMap[sortedEntries[i].Name].GT(versionMap[sortedEntries[j].Name])
			})

			skippedEntities := sets.Set[string]{}

			const (
				captureNewEntry = true
				processExisting = false
			)
			handleSemantics := func(edge string, linkID int, captureNew bool) {
				if writer.DrawV0Semantics {
					if captureNew {
						if skippedEntities.Has(edge) {
							skippedLinkIDs = append(skippedLinkIDs, fmt.Sprintf("%d", linkID))
						} else {
							skippedEntities.Insert(edge)
						}
					} else {
						if skippedEntities.Has(edge) {
							skippedLinkIDs = append(skippedLinkIDs, fmt.Sprintf("%d", linkID))
						}
					}
				}
			}

			for _, ce := range sortedEntries {
				entryID := fmt.Sprintf("%s-%s", channelID, ce.Name)
				fmt.Fprintf(pkgBuilder, "      %s[%q]\n", entryID, ce.Name)

				// mermaid allows specification of only a single decoration class, so any combinations must be independently represented
				switch {
				case depByBundle.Has(ce.Name) && skippedEntities.Has(ce.Name):
					decoratedBundleIDs["deprecatedskipped"] = append(decoratedBundleIDs["deprecatedskipped"], entryID)
				case depByBundle.Has(ce.Name):
					decoratedBundleIDs["deprecated"] = append(decoratedBundleIDs["deprecated"], entryID)
				case skippedEntities.Has(ce.Name):
					decoratedBundleIDs["skipped"] = append(decoratedBundleIDs["skipped"], entryID)
				}

				if len(ce.Skips) > 0 {
					for _, s := range ce.Skips {
						skipsID := fmt.Sprintf("%s-%s", channelID, s)
						fmt.Fprintf(pkgBuilder, "      %s[%q]-- %s --> %s[%q]\n", skipsID, s, "skip", entryID, ce.Name)
						handleSemantics(s, linkID, captureNewEntry)
						linkID++
					}
				}
				if len(ce.SkipRange) > 0 {
					skipRange, err := semver.ParseRange(ce.SkipRange)
					if err == nil {
						for _, edgeName := range filteredChannel.Entries {
							if skipRange(versionMap[edgeName.Name]) {
								skipRangeID := fmt.Sprintf("%s-%s", channelID, edgeName.Name)
								fmt.Fprintf(pkgBuilder, "      %s[%q]-- \"%s(%s)\" --> %s[%q]\n", skipRangeID, edgeName.Name, "skipRange", ce.SkipRange, entryID, ce.Name)
								handleSemantics(ce.Name, linkID, processExisting)
								linkID++
							}
						}
					} else {
						fmt.Fprintf(os.Stderr, "warning: ignoring invalid SkipRange for package/edge %q/%q: %v\n", c.Package, ce.Name, err)
					}
				}
				// have to process replaces last, because applicablity can be impacted by skips
				if len(ce.Replaces) > 0 {
					replacesID := fmt.Sprintf("%s-%s", channelID, ce.Replaces)
					fmt.Fprintf(pkgBuilder, "      %s[%q]-- %s --> %s[%q]\n", replacesID, ce.Replaces, "replace", entryID, ce.Name)
					handleSemantics(ce.Name, linkID, processExisting)
					linkID++
				}
			}
			fmt.Fprintf(pkgBuilder, "    end\n")
		}
	}

	_, _ = out.Write([]byte("graph LR\n"))
	_, _ = out.Write([]byte("  classDef deprecated fill:#E8960F\n"))
	_, _ = out.Write([]byte("  classDef skipped stroke:#FF0000,stroke-width:4px\n"))
	_, _ = out.Write([]byte("  classDef deprecatedskipped fill:#E8960F,stroke:#FF0000,stroke-width:4px\n"))
	pkgNames := []string{}
	for pname := range pkgs {
		pkgNames = append(pkgNames, pname)
	}
	sort.Slice(pkgNames, func(i, j int) bool {
		return pkgNames[i] < pkgNames[j]
	})
	for _, pkgName := range pkgNames {
		_, _ = fmt.Fprintf(out, "  %%%% package %q\n", pkgName)
		_, _ = fmt.Fprintf(out, "  subgraph %q\n", pkgName)
		_, _ = out.Write([]byte(pkgs[pkgName].String()))
		_, _ = out.Write([]byte("  end\n"))
	}

	if deprecatedPackage != "" {
		_, _ = fmt.Fprintf(out, "style %s fill:#989695\n", deprecatedPackage)
	}

	if len(deprecatedChannelIDs) > 0 {
		for _, deprecatedChannel := range deprecatedChannelIDs {
			_, _ = fmt.Fprintf(out, "style %s fill:#DCD0FF\n", deprecatedChannel)
		}
	}

	// express the decoration classes
	sortedKeys := slices.Sorted(maps.Keys(decoratedBundleIDs))
	for _, key := range sortedKeys {
		if len(decoratedBundleIDs[key]) > 0 {
			b := slices.Clone(decoratedBundleIDs[key])
			slices.Sort(b)
			_, _ = fmt.Fprintf(out, "class %s %s\n", strings.Join(b, ","), key)
		}
	}

	if len(skippedLinkIDs) > 0 {
		_, _ = fmt.Fprintf(out, "linkStyle %s %s\n", strings.Join(skippedLinkIDs, ","), "stroke:#FF0000,stroke-width:3px,stroke-dasharray:5;")
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
		// nolint:nestif
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

func configsByPackage(cfg DeclarativeConfig) (sets.Set[string], map[string]DeclarativeConfig, []Meta) {
	pkgNames := sets.New[string]()
	byCfg := map[string]DeclarativeConfig{}
	var rootOthers []Meta

	add := func(name string, fn func(DeclarativeConfig) DeclarativeConfig) {
		if name == "" {
			return
		}
		pkgNames.Insert(name)
		byCfg[name] = fn(byCfg[name])
	}

	for _, p := range cfg.Packages {
		add(p.Name, func(c DeclarativeConfig) DeclarativeConfig {
			c.Packages = append(c.Packages, p)
			return c
		})
	}
	for _, c := range cfg.Channels {
		add(c.Package, func(dc DeclarativeConfig) DeclarativeConfig {
			dc.Channels = append(dc.Channels, c)
			return dc
		})
	}
	for _, b := range cfg.Bundles {
		add(b.Package, func(c DeclarativeConfig) DeclarativeConfig {
			c.Bundles = append(c.Bundles, b)
			return c
		})
	}
	for _, o := range cfg.Others {
		if o.Package == "" {
			rootOthers = append(rootOthers, o)
			continue
		}
		add(o.Package, func(c DeclarativeConfig) DeclarativeConfig {
			c.Others = append(c.Others, o)
			return c
		})
	}
	for _, d := range cfg.Deprecations {
		add(d.Package, func(c DeclarativeConfig) DeclarativeConfig {
			c.Deprecations = append(c.Deprecations, d)
			return c
		})
	}

	return pkgNames, byCfg, rootOthers
}

func writeToEncoder(cfg DeclarativeConfig, enc encoder) error {
	pkgNames, byCfg, rootOthers := configsByPackage(cfg)

	for _, pName := range sets.List(pkgNames) {
		pkgCfg := byCfg[pName]

		for _, p := range pkgCfg.Packages {
			if err := enc.Encode(p); err != nil {
				return err
			}
		}

		channels := pkgCfg.Channels
		sort.Slice(channels, func(i, j int) bool {
			return channels[i].Name < channels[j].Name
		})
		for _, c := range channels {
			if err := enc.Encode(c); err != nil {
				return err
			}
		}

		bundles := pkgCfg.Bundles
		sort.Slice(bundles, func(i, j int) bool {
			return bundles[i].Name < bundles[j].Name
		})
		for _, b := range bundles {
			if err := enc.Encode(b); err != nil {
				return err
			}
		}

		others := pkgCfg.Others
		sort.SliceStable(others, func(i, j int) bool {
			return others[i].Schema < others[j].Schema
		})
		for _, o := range others {
			if err := enc.Encode(o); err != nil {
				return err
			}
		}

		//
		// Normally we would order the deprecations, but it really doesn't make sense since
		// - there will be 0 or 1 of them for any given package
		// - they have no other useful field for ordering
		//
		// validation is typically via conversion to a model.Model and invoking model.Package.Validate()
		// It's possible that a user of the object could create a slice containing more then 1
		// Deprecation object for a package, and it would bypass validation if this
		// function gets called without conversion.
		//
		for _, d := range pkgCfg.Deprecations {
			if err := enc.Encode(d); err != nil {
				return err
			}
		}
	}

	for _, o := range rootOthers {
		if err := enc.Encode(o); err != nil {
			return err
		}
	}

	return nil
}

type WriteFunc func(config DeclarativeConfig, w io.Writer) error

func WriteFS(cfg DeclarativeConfig, rootDir string, writeFunc WriteFunc, fileExt string) error {
	pkgNames, byCfg, rootOthers := configsByPackage(cfg)

	if err := os.MkdirAll(rootDir, 0777); err != nil {
		return err
	}

	for _, pName := range sets.List(pkgNames) {
		if !filepath.IsLocal(pName) {
			return fmt.Errorf("invalid package name %q: must be a single local path element", pName)
		}
		pkgDir := filepath.Join(rootDir, pName)
		if err := os.MkdirAll(pkgDir, 0777); err != nil {
			return err
		}
		filename := filepath.Join(pkgDir, fmt.Sprintf("catalog%s", fileExt))
		if err := writeFile(byCfg[pName], filename, writeFunc); err != nil {
			return err
		}
	}

	// Others with no package name cannot belong to any package directory;
	// write them to a root-level catalog file, consistent with writeToEncoder.
	if len(rootOthers) > 0 {
		filename := filepath.Join(rootDir, fmt.Sprintf("catalog%s", fileExt))
		if err := writeFile(DeclarativeConfig{Others: rootOthers}, filename, writeFunc); err != nil {
			return err
		}
	}

	return nil
}

func writeFile(cfg DeclarativeConfig, filename string, writeFunc WriteFunc) error {
	buf := &bytes.Buffer{}
	if err := writeFunc(cfg, buf); err != nil {
		return fmt.Errorf("write to buffer for %q: %v", filename, err)
	}
	// we explicitly want to generate content from this function which is limited only by the user's umask (G306)
	// nolint:gosec
	if err := os.WriteFile(filename, buf.Bytes(), 0666); err != nil {
		return fmt.Errorf("write file %q: %v", filename, err)
	}
	return nil
}
