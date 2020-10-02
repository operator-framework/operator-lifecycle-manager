package catalog

import (
	"encoding/json"
	"fmt"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/configmap"
	errorwrap "github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/client-go/listers/core/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
)

// ManifestResolver can dereference a manifest for a step. Steps may embed manifests directly or reference content
// in configmaps
type ManifestResolver interface {
	ManifestForStep(step *v1alpha1.Step) (string, error)
}

// manifestResolver caches manifest from unpacked bundles (via configmaps)
type manifestResolver struct {
	configMapLister v1.ConfigMapLister
	unpackedSteps   map[string][]v1alpha1.StepResource
	namespace       string
	logger          logrus.FieldLogger
}

func newManifestResolver(namespace string, configMapLister v1.ConfigMapLister, logger logrus.FieldLogger) *manifestResolver {
	return &manifestResolver{
		namespace:       namespace,
		configMapLister: configMapLister,
		unpackedSteps:   map[string][]v1alpha1.StepResource{},
		logger:          logger,
	}
}

// ManifestForStep always returns the manifest that should be applied to the cluster for a given step
// the manifest field in the installplan status can contain a reference to a configmap instead
func (r *manifestResolver) ManifestForStep(step *v1alpha1.Step) (string, error) {
	manifest := step.Resource.Manifest
	ref := refForStep(step, r.logger)
	if ref == nil {
		return manifest, nil
	}

	log := r.logger.WithFields(logrus.Fields{"resolving": step.Resolving, "step": step.Resource.Name})
	log.WithField("ref", ref).Debug("step is a reference to configmap")

	usteps, err := r.unpackedStepsForBundle(step.Resolving, ref)
	if err != nil {
		return "", err
	}

	log.Debugf("checking cache for unpacked step")
	// need to find the real manifest from the unpacked steps
	for _, u := range usteps {
		if u.Name == step.Resource.Name &&
			u.Kind == step.Resource.Kind &&
			u.Version == step.Resource.Version &&
			u.Group == step.Resource.Group {
			manifest = u.Manifest
			log.WithField("manifest", manifest).Debug("step replaced with unpacked value")
			break
		}
	}
	if manifest == step.Resource.Manifest {
		return "", fmt.Errorf("couldn't find unpacked step for %v", step)
	}
	return manifest, nil
}

func (r *manifestResolver) unpackedStepsForBundle(bundleName string, ref *UnpackedBundleReference) ([]v1alpha1.StepResource, error) {
	usteps, ok := r.unpackedSteps[bundleName]
	if ok {
		return usteps, nil
	}
	cm, err := r.configMapLister.ConfigMaps(ref.Namespace).Get(ref.Name)
	if err != nil {
		return nil, errorwrap.Wrapf(err, "error finding unpacked bundle configmap for ref %v", *ref)
	}
	loader := configmap.NewBundleLoader()
	bundle, err := loader.Load(cm)
	if err != nil {
		return nil, errorwrap.Wrapf(err, "error loading unpacked bundle configmap for ref %v", *ref)
	}

	if ref.Properties != "" {
		props, err := projection.PropertyListFromPropertiesAnnotation(ref.Properties)
		if err != nil {
			return nil, fmt.Errorf("failed to load bundle properties for %q: %w", bundle.CsvName, err)
		}
		bundle.Properties = props
	}

	steps, err := resolver.NewStepResourceFromBundle(bundle, r.namespace, ref.Replaces, ref.CatalogSourceName, ref.CatalogSourceNamespace)
	if err != nil {
		return nil, errorwrap.Wrapf(err, "error calculating steps for ref %v", *ref)
	}
	r.unpackedSteps[bundleName] = steps
	return steps, nil
}

func refForStep(step *v1alpha1.Step, log logrus.FieldLogger) *UnpackedBundleReference {
	log = log.WithFields(logrus.Fields{"resolving": step.Resolving, "step": step.Resource.Name})
	var ref UnpackedBundleReference
	if err := json.Unmarshal([]byte(step.Resource.Manifest), &ref); err != nil {
		log.Debug("step is not a reference to an unpacked bundle (this is not an error if the step is a manifest)")
		return nil
	}
	log = log.WithField("ref", ref)
	if ref.Kind != "ConfigMap" || ref.Name == "" || ref.Namespace == "" || ref.CatalogSourceName == "" || ref.CatalogSourceNamespace == "" {
		log.Debug("step is not a reference to an unpacked bundle (this is not an error if the step is a manifest)")
		return nil
	}
	return &ref
}
