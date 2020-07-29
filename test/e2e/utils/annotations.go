package utils

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
)

type AnnotationsFile struct {
	Annotations map[string]string `yaml:"annotations"`
}

func (b *Bundle) generateBundleAnnotations() (map[string]string, error) {
	mediaType, err := bundle.GetMediaType(b.BundlePath)
	if err != nil {
		return nil, fmt.Errorf("invalid bundle format: %v", err)
	}
	labels := make(map[string]string)
	labels[bundle.PackageLabel] = b.PackageName
	labels[bundle.ManifestsLabel] = "manifests/"
	labels[bundle.MetadataLabel] = "metadata/"
	labels[bundle.ChannelDefaultLabel] = b.DefaultChannel
	labels[bundle.ChannelsLabel] = strings.Join(b.Channels, ",")
	labels[bundle.MediatypeLabel] = mediaType
	return labels, nil
}

func (r *RegistryClient) GetAnnotations(b *Bundle) (map[string]string, error) {
	if b == nil {
		return nil, fmt.Errorf("nil bundle")
	}
	metadataDir := path.Join(b.BundlePath, "metadata")
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
		annotations, err := b.generateBundleAnnotations()
		if err != nil {
			return nil, fmt.Errorf("failed to generate bundle annotations: %v", err)
		}
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
	b.PackageName = annotations.Annotations[bundle.PackageLabel]
	b.DefaultChannel = annotations.Annotations[bundle.ChannelDefaultLabel]
	b.Channels = strings.Split(annotations.Annotations[bundle.ChannelsLabel], ", ")
	return annotations.Annotations, nil
}
