package configmap

import (
	"errors"
	"fmt"
	libbundle "github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"strings"
)

func NewBundleLoader() *BundleLoader {
	logger := logrus.NewEntry(logrus.New())
	return NewBundleLoaderWithLogger(logger)
}

func NewBundleLoaderWithLogger(logger *logrus.Entry) *BundleLoader {
	return &BundleLoader{
		logger: logger,
	}
}

// Manifest contains a bundle and a PackageManifest.
type Manifest struct {
	Bundle          *registry.Bundle
	PackageManifest *registry.PackageManifest
}

type BundleLoader struct {
	logger *logrus.Entry
}

// Load accepts a ConfigMap object, iterates through the Data section and
// creates an operator registry Bundle object.
// If the Data section has a PackageManifest resource then it is also
// deserialized and included in the result.
func (l *BundleLoader) Load(cm *corev1.ConfigMap) (manifest *Manifest, err error) {
	if cm == nil {
		err = errors.New("ConfigMap must not be <nil>")
		return
	}

	logger := l.logger.WithFields(logrus.Fields{
		"configmap": fmt.Sprintf("%s/%s", cm.GetNamespace(), cm.GetName()),
	})

	bundle, _, bundleErr := loadBundle(logger, cm.Data)
	if bundleErr != nil {
		err = fmt.Errorf("failed to extract bundle from configmap - %v", bundleErr)
		return
	}

	// get package manifest information from required annotations
	annotations := cm.GetAnnotations()
	if len(annotations) == 0 {
		err = fmt.Errorf("missing required annoations on configmap %v", cm.GetName())
		return
	}

	switch mediatype := annotations[libbundle.MediatypeLabel]; mediatype {
	case "registry+v1":
		// supported, proceed
	default:
		err = fmt.Errorf("failed to parse annotations due to unsupported media type %v", mediatype)
		return
	}

	var packageChannels []registry.PackageChannel
	channels := strings.Split(annotations[libbundle.ChannelsLabel], ",")
	for _, channel := range channels {
		packageChannels = append(packageChannels, registry.PackageChannel{
			Name: channel,
		})
	}

	manifest = &Manifest{
		Bundle: bundle,
		PackageManifest: &registry.PackageManifest{
			PackageName:        annotations[libbundle.PackageLabel],
			Channels:           packageChannels,
			DefaultChannelName: annotations[libbundle.ChannelDefaultLabel],
		},
	}
	return
}

func loadBundle(entry *logrus.Entry, data map[string]string) (bundle *registry.Bundle, skipped map[string]string, err error) {
	bundle = &registry.Bundle{}
	skipped = map[string]string{}

	// Add kube resources to the bundle.
	for name, content := range data {
		reader := strings.NewReader(content)
		logger := entry.WithFields(logrus.Fields{
			"key": name,
		})

		resource, decodeErr := registry.DecodeUnstructured(reader)
		if decodeErr != nil {
			logger.Infof("skipping due to decode error - %v", decodeErr)

			// It may not be not a kube resource, let's add it to the skipped
			// list so the caller can act on ot.
			skipped[name] = content
			continue
		}

		// It's a valid kube resource,
		// could be a crd, csv or other raw kube manifest(s).
		bundle.Add(resource)
		logger.Infof("added to bundle, Kind=%s", resource.GetKind())
	}

	return
}

func loadPackageManifest(entry *logrus.Entry, resources map[string]string) *registry.PackageManifest {
	// Let's inspect if any of the skipped non kube resources is a PackageManifest type.
	// The first one we run into will be selected.
	for name, content := range resources {
		logger := entry.WithFields(logrus.Fields{
			"key": name,
		})

		// Is it a package yaml file?
		reader := strings.NewReader(content)
		packageManifest, decodeErr := registry.DecodePackageManifest(reader)
		if decodeErr != nil {
			logger.Infof("skipping, not a PackageManifest type - %v", decodeErr)
			continue
		}

		logger.Infof("found a PackageManifest type resource - packageName=%s", packageManifest.PackageName)

		return packageManifest
	}

	return nil
}

func extract(data map[string]string) []string {
	resources := make([]string, 0)
	for _, v := range data {
		resources = append(resources, v)
	}

	return resources
}
