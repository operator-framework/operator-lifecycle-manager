package bundle

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"path"
	"strings"
)

const (
	mediatypeLabel      = "operators.operatorframework.io.bundle.mediatype.v1"
	manifestsLabel      = "operators.operatorframework.io.bundle.manifests.v1"
	metadataLabel       = "operators.operatorframework.io.bundle.metadata.v1"
	packageLabel        = "operators.operatorframework.io.bundle.package.v1"
	channelsLabel       = "operators.operatorframework.io.bundle.channels.v1"
	defaultChannelLabel = "operators.operatorframework.io.bundle.channel.default.v1"
	mediatypeRegistry   = "registry+v1"
)

type AnnotationsFile struct {
	Annotations map[string]string `yaml:"annotations"`
}

func (b *Bundle) generateBundleAnnotations() map[string]string {
	if len(b.BundleManifestDirectory) == 0 {
		b.BundleManifestDirectory = "manifests/"
	}
	labels := make(map[string]string)
	labels[mediatypeLabel] = mediatypeRegistry
	labels[packageLabel] = b.PackageName
	labels[manifestsLabel] = b.BundleManifestDirectory
	labels[metadataLabel] = "metadata/"
	labels[defaultChannelLabel] = b.DefaultChannel
	labels[channelsLabel] = strings.Join(b.Channels, ",")
	return labels
}

func (r *RegistryClient) GetAnnotations(b *Bundle) (map[string]string, error) {
	if b == nil {
		return nil, fmt.Errorf("nil bundle")
	}
	metadataDir := path.Join(b.BundleDir, "metadata")
	annotationsFile := path.Join(metadataDir, "annotations.yaml")

	// Generate an annotations.yaml file from bundle information
	if b.GenerateAnnotations {
		f, err := os.Stat(metadataDir)
		if os.IsNotExist(err) {
			if err := os.MkdirAll(metadataDir, 0755); err != nil {
				return nil, fmt.Errorf("Unable to create metadata directory for bundle %s: %v", b.PackageName, err)
			}
		}
		if f != nil && !f.IsDir() {
			return nil, fmt.Errorf("%s already present and not a directory", metadataDir)
		}
		annotations := b.generateBundleAnnotations()
		annotationsStruct := AnnotationsFile{Annotations: annotations}
		annotationsRaw, err := yaml.Marshal(annotationsStruct)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal generated annotations")
		}
		file, err := os.OpenFile(annotationsFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
		if err != nil {
			return nil, fmt.Errorf("error opening annotations file: %v", err)
		}
		defer file.Close()
		if _, err := file.Write(annotationsRaw); err != nil {
			return nil, fmt.Errorf("error writing to annotations file: %v", err)
		}
		return annotations, nil
	}

	// read from an existing annotations.yaml file
	f, err := os.OpenFile(annotationsFile, os.O_RDONLY, 0666)
	if err != nil {
		return nil, fmt.Errorf("could not read annotations file: %v", err)
	}
	defer f.Close()
	annotationsRaw, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("error reading from annotations file: %v", err)
	}
	annotations := AnnotationsFile{}
	if err := yaml.Unmarshal(annotationsRaw, &annotations); err != nil {
		return nil, fmt.Errorf("error unmarshalling from annotations file", err)
	}
	b.PackageName = annotations.Annotations[packageLabel]
	b.DefaultChannel = annotations.Annotations[defaultChannelLabel]
	b.Channels = strings.Split(annotations.Annotations[channelsLabel], ", ")
	return annotations.Annotations, nil
}
