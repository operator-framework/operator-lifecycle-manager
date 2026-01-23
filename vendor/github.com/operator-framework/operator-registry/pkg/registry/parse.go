package registry

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

type bundleParser struct {
	log *logrus.Entry
}

func newBundleParser(log *logrus.Entry) *bundleParser {
	return &bundleParser{
		log: log,
	}
}

// Parse parses the given FS into a Bundle.
func (b *bundleParser) Parse(root fs.FS) (*Bundle, error) {
	if root == nil {
		return nil, fmt.Errorf("filesystem is nil")
	}

	bundle := &Bundle{}
	manifests, err := fs.Sub(root, "manifests")
	if err != nil {
		return nil, fmt.Errorf("error opening manifests directory: %s", err)
	}
	if err := b.addManifests(manifests, bundle); err != nil {
		return nil, err
	}

	metadata, err := fs.Sub(root, "metadata")
	if err != nil {
		return nil, fmt.Errorf("error opening metadata directory: %s", err)
	}
	if err := b.addMetadata(metadata, bundle); err != nil {
		return nil, err
	}

	derived, err := b.derivedProperties(bundle)
	if err != nil {
		return nil, fmt.Errorf("failed to derive properties: %s", err)
	}

	bundle.Properties = propertySet(append(bundle.Properties, derived...))

	return bundle, nil
}

// addManifests adds the result of parsing the manifests directory to a bundle.
func (b *bundleParser) addManifests(manifests fs.FS, bundle *Bundle) error {
	files, err := fs.ReadDir(manifests, ".")
	if err != nil {
		return err
	}

	var csvFound bool
	for _, f := range files {
		if f.IsDir() {
			continue
		}

		name := f.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}

		obj := &unstructured.Unstructured{}
		if err = decodeFileFS(manifests, name, obj, b.log); err != nil {
			b.log.Warnf("failed to decode: %s", err)
			continue
		}

		// Only include the first CSV we find in the
		if obj.GetKind() == operatorsv1alpha1.ClusterServiceVersionKind {
			if csvFound {
				continue
			}
			csvFound = true
		}

		if obj.Object != nil {
			bundle.Add(obj)
		}
	}

	if bundle.Size() == 0 {
		return fmt.Errorf("no bundle objects found")
	}

	csv, err := bundle.ClusterServiceVersion()
	if err != nil {
		return err
	}
	if csv == nil {
		return fmt.Errorf("no csv in bundle")
	}

	bundle.Name = csv.GetName()
	if err := bundle.AllProvidedAPIsInBundle(); err != nil {
		return fmt.Errorf("error checking provided apis in bundle %s: %s", bundle.Name, err)
	}

	return nil
}

// addManifests adds the result of parsing the metadata directory to a bundle.
func (b *bundleParser) addMetadata(metadata fs.FS, bundle *Bundle) error {
	files, err := fs.ReadDir(metadata, ".")
	if err != nil {
		return err
	}

	var (
		af *AnnotationsFile
		df *DependenciesFile
		pf *PropertiesFile
	)
	for _, f := range files {
		name := f.Name()
		if af == nil {
			decoded := AnnotationsFile{}
			if err = decodeFileFS(metadata, name, &decoded, b.log); err == nil {
				if decoded != (AnnotationsFile{}) {
					af = &decoded
				}
			}
		}
		if df == nil {
			decoded := DependenciesFile{}
			if err = decodeFileFS(metadata, name, &decoded, b.log); err == nil {
				if len(decoded.Dependencies) > 0 {
					df = &decoded
				}
			}
		}
		if pf == nil {
			decoded := PropertiesFile{}
			if err = decodeFileFS(metadata, name, &decoded, b.log); err == nil {
				if len(decoded.Properties) > 0 {
					pf = &decoded
				}
			}
		}
	}

	if af != nil {
		bundle.Annotations = &af.Annotations
		bundle.Package = af.Annotations.PackageName
		bundle.Channels = af.GetChannels()
	} else {
		//nolint:staticcheck // ST1005: error message is intentionally capitalized
		return fmt.Errorf("Could not find annotations file")
	}

	if df != nil {
		bundle.Dependencies = append(bundle.Dependencies, df.GetDependencies()...)
	} else {
		b.log.Info("Could not find optional dependencies file")
	}

	if pf != nil {
		bundle.Properties = append(bundle.Properties, pf.Properties...)
	} else {
		b.log.Info("Could not find optional properties file")
	}

	return nil
}

func (b *bundleParser) derivedProperties(bundle *Bundle) ([]Property, error) {
	// Add properties from CSV annotations
	csv, err := bundle.ClusterServiceVersion()
	if err != nil {
		return nil, fmt.Errorf("error getting csv: %s", err)
	}
	if csv == nil {
		return nil, fmt.Errorf("bundle missing csv")
	}

	// nolint:prealloc
	var derived []Property
	if len(csv.GetAnnotations()) > 0 {
		properties, ok := csv.GetAnnotations()[PropertyKey]
		if ok {
			if err := json.Unmarshal([]byte(properties), &derived); err != nil {
				b.log.Warnf("failed to unmarshal csv annotation properties: %s", err)
			}
		}
	}

	// nolint:nestif
	// existing code triggering nested complexity, but at least will not make worse with release processing
	if bundle.Annotations != nil && bundle.Annotations.PackageName != "" {
		pkg := bundle.Annotations.PackageName
		version, err := bundle.Version()
		if err != nil {
			return nil, err
		}
		release, err := bundle.Release()
		if err != nil {
			return nil, err
		}
		if release == "" && csv.GetSubstitutesFor() != "" {
			version, release, err = extractReleaseVersionFromBuildMetadata(version)
			if err != nil {
				return nil, fmt.Errorf("bundle %q error: %v", bundle.Name, err)
			}
		}

		value, err := json.Marshal(PackageProperty{
			PackageName: pkg,
			Version:     version,
			Release:     release,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal package property: %s", err)
		}

		// Annotations file takes precedence over CSV annotations
		derived = append([]Property{{Type: PackageType, Value: value}}, derived...)
	}

	providedAPIs, err := bundle.ProvidedAPIs()
	if err != nil {
		return nil, fmt.Errorf("error getting provided apis: %s", err)
	}

	for api := range providedAPIs {
		value, err := json.Marshal(GVKProperty{
			Group:   api.Group,
			Kind:    api.Kind,
			Version: api.Version,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal gvk property: %s", err)
		}
		derived = append(derived, Property{Type: GVKType, Value: value})
	}

	return propertySet(derived), nil
}

// propertySet returns the deduplicated set of a property list.
func propertySet(properties []Property) []Property {
	// nolint:prealloc
	var (
		set     []Property
		visited = map[string]struct{}{}
	)
	for _, p := range properties {
		if _, ok := visited[p.String()]; ok {
			continue
		}
		visited[p.String()] = struct{}{}
		set = append(set, p)
	}

	return set
}

func extractReleaseVersionFromBuildMetadata(substitutesFor string) (string, string, error) {
	var version, release string
	// if the bundle expresses no release version, but
	// includes the substitutesFor annotation, then we
	// interpret any build metadata in the version as
	// the release version.
	// failure to parse build metadata under these conditions is fatal,
	// though validation is later
	parts := strings.SplitN(substitutesFor, "+", 2)
	if len(parts) == 2 {
		version = parts[0]
		release = parts[1]
	} else {
		return "", "", fmt.Errorf("no release version expressed as build metadata: %q", substitutesFor)
	}
	return version, release, nil
}
