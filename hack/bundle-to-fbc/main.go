// bundle-to-fbc generates or refreshes inline olm.bundle.object properties
// in File-Based Catalog (FBC) YAML files.
//
// Two modes:
//
//	PRINT mode (default): pull a bundle image, encode its manifests as
//	olm.bundle.object properties using property.MustBuildBundleObject, and
//	print them to stdout.
//
//	  go run ./hack/bundle-to-fbc <bundle-image> [--replace OLD=NEW ...]
//
//	UPDATE mode: update an FBC file in-place.  Behaviour depends on whether
//	the olm.bundle stanza has an image: field:
//
//	  Has image: field  → pull that bundle image (or --image if overriding),
//	                      generate fresh olm.bundle.object properties, remove
//	                      the image: field, and write the file.
//
//	  No image: field   → decode existing olm.bundle.object blobs, apply
//	                      --replace substitutions, re-encode using
//	                      MustBuildBundleObject, and write.  No image pull.
//	                      Formatting is preserved via text replacement.
//
//	  go run ./hack/bundle-to-fbc --update <fbc-file> [--image <img>] [--replace OLD=NEW ...]
package main

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/operator-framework/operator-registry/alpha/property"
	"gopkg.in/yaml.v3"
	sigsyaml "sigs.k8s.io/yaml"
)

// ---------------------------------------------------------------------------
// CLI flags
// ---------------------------------------------------------------------------

type stringsFlag []string

func (s *stringsFlag) String() string { return strings.Join(*s, ", ") }
func (s *stringsFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func parseReplacements(raw []string) (map[string]string, error) {
	m := make(map[string]string, len(raw))
	for _, r := range raw {
		parts := strings.SplitN(r, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("--replace must be OLD=NEW, got %q", r)
		}
		m[parts[0]] = parts[1]
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Image extraction
// ---------------------------------------------------------------------------

func pullAndExtract(image, destDir string) error {
	fmt.Fprintf(os.Stderr, "[bundle-to-fbc] Pulling %s\n", image)
	pull := exec.Command("docker", "pull", "-q", image)
	pull.Stderr = os.Stderr
	pull.Stdout = io.Discard
	if err := pull.Run(); err != nil {
		return fmt.Errorf("docker pull %s: %w", image, err)
	}

	save := exec.Command("docker", "save", image)
	tarData, err := save.Output()
	if err != nil {
		return fmt.Errorf("docker save %s: %w", image, err)
	}

	if err := extractTar(bytes.NewReader(tarData), destDir); err != nil {
		return fmt.Errorf("extracting image tar: %w", err)
	}

	manifestData, err := os.ReadFile(filepath.Join(destDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("reading manifest.json: %w", err)
	}
	var manifests []struct {
		Layers []string `json:"Layers"`
	}
	if err := json.Unmarshal(manifestData, &manifests); err != nil {
		return fmt.Errorf("parsing manifest.json: %w", err)
	}
	if len(manifests) == 0 {
		return fmt.Errorf("manifest.json has no entries")
	}

	for _, layer := range manifests[0].Layers {
		f, err := os.Open(filepath.Join(destDir, layer))
		if err != nil {
			return fmt.Errorf("opening layer %s: %w", layer, err)
		}
		err = extractTar(f, destDir)
		f.Close()
		if err != nil {
			return fmt.Errorf("extracting layer %s: %w", layer, err)
		}
	}
	return nil
}

func extractTar(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Guard against path traversal.
		path := filepath.Join(destDir, filepath.Clean("/"+hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func manifestFiles(bundleDir string) ([]string, error) {
	manifestsDir := filepath.Join(bundleDir, "manifests")
	entries, err := os.ReadDir(manifestsDir)
	if err != nil {
		return nil, fmt.Errorf("reading manifests/: %w", err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".json") {
			files = append(files, filepath.Join(manifestsDir, name))
		}
	}
	sort.Strings(files)
	return files, nil
}

// ---------------------------------------------------------------------------
// Property building
// ---------------------------------------------------------------------------

// bundleObjectNode builds a yaml.Node for one olm.bundle.object property.
// The value mapping is emitted in flow style: {data: BASE64}
func bundleObjectNode(manifestJSON []byte, replacements map[string]string) (*yaml.Node, error) {
	s := applyReplacements(string(manifestJSON), replacements)

	// MustBuildBundleObject encodes the manifest JSON bytes as base64 inside
	// a {"data": "BASE64"} JSON value.
	prop := property.MustBuildBundleObject([]byte(s))

	var obj struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(prop.Value, &obj); err != nil {
		return nil, fmt.Errorf("unmarshaling bundle object value: %w", err)
	}

	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			scalar("type"),
			scalar(property.TypeBundleObject),
			scalar("value"),
			{
				Kind:  yaml.MappingNode,
				Style: yaml.FlowStyle,
				Content: []*yaml.Node{
					scalar("data"),
					scalar(obj.Data),
				},
			},
		},
	}, nil
}

func scalar(val string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: val}
}

func applyReplacements(s string, replacements map[string]string) string {
	for old, newval := range replacements {
		s = strings.ReplaceAll(s, old, newval)
	}
	return s
}

// propsFromImage pulls a bundle image and returns yaml.Nodes for all
// olm.bundle.object properties it encodes.
func propsFromImage(image string, replacements map[string]string) ([]*yaml.Node, error) {
	tmpDir, err := os.MkdirTemp("", "bundle-to-fbc-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	if err := pullAndExtract(image, tmpDir); err != nil {
		return nil, err
	}

	files, err := manifestFiles(tmpDir)
	if err != nil {
		return nil, err
	}

	var nodes []*yaml.Node
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}

		var meta struct {
			Kind string `yaml:"kind"`
		}
		_ = yaml.Unmarshal(raw, &meta)
		fmt.Fprintf(os.Stderr, "[bundle-to-fbc]   encoding %s (%s)\n", filepath.Base(f), meta.Kind)

		// Convert YAML → compact JSON.
		j, err := sigsyaml.YAMLToJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("converting %s to JSON: %w", f, err)
		}
		var compactBuf bytes.Buffer
		if err := json.Compact(&compactBuf, j); err != nil {
			return nil, fmt.Errorf("compacting JSON for %s: %w", f, err)
		}

		node, err := bundleObjectNode(compactBuf.Bytes(), replacements)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// ---------------------------------------------------------------------------
// YAML node helpers
// ---------------------------------------------------------------------------

// mappingGet returns the value node for key in a MappingNode's Content.
func mappingGet(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// mappingDel removes a key-value pair from a MappingNode's Content.
func mappingDel(n *yaml.Node, key string) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content = append(n.Content[:i], n.Content[i+2:]...)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// FBC file update — two strategies
// ---------------------------------------------------------------------------

// patchInPlace updates olm.bundle.object blobs using text replacement only,
// preserving all YAML formatting (flow-style values, list indentation, etc.).
func patchInPlace(path string, replacements map[string]string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	dec := yaml.NewDecoder(bytes.NewReader(content))
	blobMap := map[string]string{} // old base64 → new base64
	total := 0

	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		mapping := doc.Content[0]
		schema := mappingGet(mapping, "schema")
		if schema == nil || schema.Value != "olm.bundle" {
			continue
		}
		name := mappingGet(mapping, "name")
		propsNode := mappingGet(mapping, "properties")
		if propsNode == nil {
			continue
		}

		count := 0
		for _, item := range propsNode.Content {
			t := mappingGet(item, "type")
			if t == nil || t.Value != property.TypeBundleObject {
				continue
			}
			count++
			total++
			valNode := mappingGet(item, "value")
			if valNode == nil {
				continue
			}
			dataNode := mappingGet(valNode, "data")
			if dataNode == nil {
				continue
			}
			oldData := dataNode.Value

			decoded, err := base64.StdEncoding.DecodeString(oldData)
			if err != nil {
				return fmt.Errorf("decoding blob for bundle %q: %w", name.Value, err)
			}

			patched := applyReplacements(string(decoded), replacements)

			// Re-compact through JSON to normalise.
			var buf bytes.Buffer
			if err := json.Compact(&buf, []byte(patched)); err != nil {
				return fmt.Errorf("re-compacting JSON for bundle %q: %w", name.Value, err)
			}

			prop := property.MustBuildBundleObject(buf.Bytes())
			var obj struct {
				Data string `json:"data"`
			}
			_ = json.Unmarshal(prop.Value, &obj)

			if obj.Data != oldData {
				blobMap[oldData] = obj.Data
			}
		}

		bundleName := "<unknown>"
		if name != nil {
			bundleName = name.Value
		}
		if count == 0 {
			fmt.Fprintf(os.Stderr, "[bundle-to-fbc] WARNING: bundle %q has no image: and no olm.bundle.object properties\n", bundleName)
		} else {
			fmt.Fprintf(os.Stderr, "[bundle-to-fbc] %s: patching %d existing blob(s) for bundle %q\n", path, count, bundleName)
		}
	}

	if len(blobMap) == 0 {
		fmt.Fprintf(os.Stderr, "[bundle-to-fbc] %s: %d blob(s) — no changes needed\n", path, total)
		return nil
	}

	s := string(content)
	for old, newval := range blobMap {
		s = strings.ReplaceAll(s, old, newval)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[bundle-to-fbc] wrote %s (%d/%d blob(s) patched)\n", path, len(blobMap), total)
	return nil
}

// updateWithImages pulls bundle images and rewrites olm.bundle stanzas with
// inline olm.bundle.object properties.  Uses a full yaml.Node round-trip so
// new properties are serialized in flow style.
func updateWithImages(path, overrideImage string, replacements map[string]string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var docs []*yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(content))
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		docs = append(docs, &doc)
	}

	for _, doc := range docs {
		mapping := doc.Content[0]
		schema := mappingGet(mapping, "schema")
		if schema == nil || schema.Value != "olm.bundle" {
			continue
		}

		name := mappingGet(mapping, "name")
		imageNode := mappingGet(mapping, "image")

		sourceImage := overrideImage
		if sourceImage == "" && imageNode != nil {
			sourceImage = imageNode.Value
		}
		if sourceImage == "" {
			continue
		}

		bundleName := "<unknown>"
		if name != nil {
			bundleName = name.Value
		}
		fmt.Fprintf(os.Stderr, "[bundle-to-fbc] %s: pulling %s for bundle %q\n", path, sourceImage, bundleName)

		newProps, err := propsFromImage(sourceImage, replacements)
		if err != nil {
			return err
		}

		mappingDel(mapping, "image")

		propsNode := mappingGet(mapping, "properties")
		if propsNode == nil {
			mapping.Content = append(mapping.Content,
				scalar("properties"),
				&yaml.Node{Kind: yaml.SequenceNode, Content: newProps},
			)
		} else {
			// Keep non-bundle-object properties, append new ones.
			var kept []*yaml.Node
			for _, item := range propsNode.Content {
				t := mappingGet(item, "type")
				if t != nil && t.Value == property.TypeBundleObject {
					continue
				}
				kept = append(kept, item)
			}
			propsNode.Content = append(kept, newProps...)
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			return err
		}
	}
	if err := enc.Close(); err != nil {
		return err
	}

	out := buf.Bytes()
	// yaml.v3 omits --- before the first document; restore it if the
	// original file had one (all FBC files use explicit document markers).
	if bytes.HasPrefix(content, []byte("---")) && !bytes.HasPrefix(out, []byte("---")) {
		out = append([]byte("---\n"), out...)
	}

	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[bundle-to-fbc] wrote %s\n", path)
	return nil
}

// updateFBCFile dispatches to patchInPlace or updateWithImages based on
// whether any olm.bundle stanza has (or needs) an image: field.
func updateFBCFile(path, overrideImage string, replacements map[string]string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	needsImagePull := overrideImage != ""
	if !needsImagePull {
		dec := yaml.NewDecoder(bytes.NewReader(content))
		for {
			var doc yaml.Node
			if err := dec.Decode(&doc); err == io.EOF {
				break
			} else if err != nil {
				return err
			}
			mapping := doc.Content[0]
			schema := mappingGet(mapping, "schema")
			if schema == nil || schema.Value != "olm.bundle" {
				continue
			}
			if mappingGet(mapping, "image") != nil {
				needsImagePull = true
				break
			}
		}
	}

	if needsImagePull {
		return updateWithImages(path, overrideImage, replacements)
	}
	return patchInPlace(path, replacements)
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	var (
		updateFile string
		image      string
		replaces   stringsFlag
	)

	flags := newFlagSet()
	flags.StringVar(&updateFile, "update", "", "Update an FBC file in-place instead of printing to stdout")
	flags.StringVar(&image, "image", "", "Override/provide the source bundle image when using --update")
	flags.Var(&replaces, "replace", "String replacement applied inside manifest JSON before encoding OLD=NEW (repeatable)")

	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	replacements, err := parseReplacements(replaces)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	switch {
	case updateFile != "":
		if err := updateFBCFile(updateFile, image, replacements); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	case flags.NArg() == 1:
		bundleImage := flags.Arg(0)
		nodes, err := propsFromImage(bundleImage, replacements)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, n := range nodes {
			dataNode := mappingGet(mappingGet(n, "value"), "data")
			fmt.Printf("  - type: %s\n", property.TypeBundleObject)
			fmt.Printf("    value: {data: %s}\n", dataNode.Value)
		}

	default:
		fmt.Fprintln(os.Stderr, "usage: bundle-to-fbc [--update <file>] [--image <img>] [--replace OLD=NEW] [bundle-image]")
		os.Exit(2)
	}
}

func newFlagSet() *flag.FlagSet {
	return flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
}
