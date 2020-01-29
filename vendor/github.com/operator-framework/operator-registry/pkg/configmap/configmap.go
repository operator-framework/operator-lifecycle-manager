package configmap

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/registry"
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

type BundleLoader struct {
	logger *logrus.Entry
}

// Load accepts a ConfigMap object, iterates through the Data section and
// creates an operator registry Bundle object.
// If the Data section has a PackageManifest resource then it is also
// deserialized and included in the result.
func (l *BundleLoader) Load(cm *corev1.ConfigMap) (bundle *api.Bundle, err error) {
	if cm == nil {
		err = errors.New("ConfigMap must not be <nil>")
		return
	}

	logger := l.logger.WithFields(logrus.Fields{
		"configmap": fmt.Sprintf("%s/%s", cm.GetNamespace(), cm.GetName()),
	})

	bundle, skipped, bundleErr := loadBundle(logger, cm.Data)
	if bundleErr != nil {
		err = fmt.Errorf("failed to extract bundle from configmap - %v", bundleErr)
		return
	}
	l.logger.Debugf("couldn't unpack skipped: %#v", skipped)
	return
}

func loadBundle(entry *logrus.Entry, data map[string]string) (bundle *api.Bundle, skipped map[string]string, err error) {
	bundle = &api.Bundle{Object: []string{}}
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

		if resource.GetKind() == "ClusterServiceVersion" {
			csvBytes, err := resource.MarshalJSON()
			if err != nil {
				return nil, nil, err
			}
			bundle.CsvJson = string(csvBytes)
			bundle.CsvName = resource.GetName()
		}
		bundle.Object = append(bundle.Object, content)
		logger.Infof("added to bundle, Kind=%s", resource.GetKind())
	}

	return
}
