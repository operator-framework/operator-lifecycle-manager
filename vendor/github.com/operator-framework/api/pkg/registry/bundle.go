package registry

import (
	"fmt"
	"strings"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Scheme is the default instance of runtime.Scheme to which types in the Kubernetes API are already registered.
var Scheme = runtime.NewScheme()

// Codecs provides access to encoding and decoding for the scheme
var Codecs = serializer.NewCodecFactory(Scheme)

func DefaultYAMLDecoder() runtime.Decoder {
	return Codecs.UniversalDeserializer()
}

func init() {
	if err := v1beta1.AddToScheme(Scheme); err != nil {
		panic(err)
	}
}

type Bundle struct {
	Name        string
	Objects     []*unstructured.Unstructured
	Package     string
	Channel     string
	BundleImage string
	csv         *ClusterServiceVersion
	crds        []*v1beta1.CustomResourceDefinition
	cacheStale  bool
}

func NewBundle(name, pkgName, channelName string, objs ...*unstructured.Unstructured) *Bundle {
	bundle := &Bundle{Name: name, Package: pkgName, Channel: channelName, cacheStale: false}
	for _, o := range objs {
		bundle.Add(o)
	}
	return bundle
}

func NewBundleFromStrings(name, pkgName, channelName string, objs []string) (*Bundle, error) {
	unstObjs := []*unstructured.Unstructured{}
	for _, o := range objs {
		dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(o), 10)
		unst := &unstructured.Unstructured{}
		if err := dec.Decode(unst); err != nil {
			return nil, err
		}
		unstObjs = append(unstObjs, unst)
	}
	return NewBundle(name, pkgName, channelName, unstObjs...), nil
}

func (b *Bundle) Size() int {
	return len(b.Objects)
}

func (b *Bundle) Add(obj *unstructured.Unstructured) {
	b.Objects = append(b.Objects, obj)
	b.cacheStale = true
}

func (b *Bundle) ClusterServiceVersion() (*ClusterServiceVersion, error) {
	if err := b.cache(); err != nil {
		return nil, err
	}
	return b.csv, nil
}

func (b *Bundle) Version() (string, error) {
	if err := b.cache(); err != nil {
		return "", err
	}
	return b.csv.GetVersion()
}

func (b *Bundle) SkipRange() (string, error) {
	if err := b.cache(); err != nil {
		return "", err
	}
	return b.csv.GetSkipRange(), nil
}

func (b *Bundle) CustomResourceDefinitions() ([]*v1beta1.CustomResourceDefinition, error) {
	if err := b.cache(); err != nil {
		return nil, err
	}
	return b.crds, nil
}

func (b *Bundle) ProvidedAPIs() (map[APIKey]struct{}, error) {
	provided := map[APIKey]struct{}{}
	crds, err := b.CustomResourceDefinitions()
	if err != nil {
		return nil, err
	}
	for _, crd := range crds {
		for _, v := range crd.Spec.Versions {
			provided[APIKey{Group: crd.Spec.Group, Version: v.Name, Kind: crd.Spec.Names.Kind, Plural: crd.Spec.Names.Plural}] = struct{}{}
		}
		if crd.Spec.Version != "" {
			provided[APIKey{Group: crd.Spec.Group, Version: crd.Spec.Version, Kind: crd.Spec.Names.Kind, Plural: crd.Spec.Names.Plural}] = struct{}{}
		}
	}

	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return nil, err
	}

	ownedAPIs, _, err := csv.GetApiServiceDefinitions()
	if err != nil {
		return nil, err
	}
	for _, api := range ownedAPIs {
		provided[APIKey{Group: api.Group, Version: api.Version, Kind: api.Kind, Plural: api.Name}] = struct{}{}
	}
	return provided, nil
}

func (b *Bundle) RequiredAPIs() (map[APIKey]struct{}, error) {
	required := map[APIKey]struct{}{}
	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return nil, err
	}

	_, requiredCRDs, err := csv.GetCustomResourceDefintions()
	if err != nil {
		return nil, err
	}
	for _, api := range requiredCRDs {
		parts := strings.SplitN(api.Name, ".", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("couldn't parse plural.group from crd name: %s", api.Name)
		}
		required[APIKey{parts[1], api.Version, api.Kind, parts[0]}] = struct{}{}

	}
	_, requiredAPIs, err := csv.GetApiServiceDefinitions()
	if err != nil {
		return nil, err
	}
	for _, api := range requiredAPIs {
		required[APIKey{Group: api.Group, Version: api.Version, Kind: api.Kind, Plural: api.Name}] = struct{}{}
	}
	return required, nil
}

func (b *Bundle) AllProvidedAPIsInBundle() error {
	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return err
	}
	bundleAPIs, err := b.ProvidedAPIs()
	if err != nil {
		return err
	}
	ownedCRDs, _, err := csv.GetCustomResourceDefintions()
	if err != nil {
		return err
	}
	shouldExist := make(map[APIKey]struct{}, len(ownedCRDs))
	for _, crdDef := range ownedCRDs {
		parts := strings.SplitN(crdDef.Name, ".", 2)
		if len(parts) < 2 {
			return fmt.Errorf("couldn't parse plural.group from crd name: %s", crdDef.Name)
		}
		shouldExist[APIKey{parts[1], crdDef.Version, crdDef.Kind, parts[0]}] = struct{}{}
	}
	for key := range shouldExist {
		if _, ok := bundleAPIs[key]; !ok {
			return fmt.Errorf("couldn't find %v in bundle. found: %v", key, bundleAPIs)
		}
	}
	// note: don't need to check bundle for extension apiserver types, which don't require extra bundle entries
	return nil
}

func (b *Bundle) Serialize() (csvName, bundleImage string, csvBytes []byte, bundleBytes []byte, err error) {
	csvCount := 0
	for _, obj := range b.Objects {
		objBytes, err := runtime.Encode(unstructured.UnstructuredJSONScheme, obj)
		if err != nil {
			return "", "", nil, nil, err
		}
		bundleBytes = append(bundleBytes, objBytes...)

		if obj.GetObjectKind().GroupVersionKind().Kind == "ClusterServiceVersion" {
			csvName = obj.GetName()
			csvBytes, err = runtime.Encode(unstructured.UnstructuredJSONScheme, obj)
			if err != nil {
				return "", "", nil, nil, err
			}
			csvCount += 1
			if csvCount > 1 {
				return "", "", nil, nil, fmt.Errorf("two csvs found in one bundle")
			}
		}
	}

	return csvName, b.BundleImage, csvBytes, bundleBytes, nil
}

func (b *Bundle) Images() (map[string]struct{}, error) {
	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return nil, err
	}

	images, err := csv.GetOperatorImages()
	if err != nil {
		return nil, err
	}

	relatedImages, err := csv.GetRelatedImages()
	if err != nil {
		return nil, err
	}
	for img := range relatedImages {
		images[img] = struct{}{}
	}

	return images, nil
}

func (b *Bundle) cache() error {
	if !b.cacheStale {
		return nil
	}
	for _, o := range b.Objects {
		if o.GetObjectKind().GroupVersionKind().Kind == "ClusterServiceVersion" {
			csv := &ClusterServiceVersion{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.UnstructuredContent(), csv); err != nil {
				return err
			}
			b.csv = csv
			break
		}
	}

	if b.crds == nil {
		b.crds = []*v1beta1.CustomResourceDefinition{}
	}
	for _, o := range b.Objects {
		if o.GetObjectKind().GroupVersionKind().Kind == "CustomResourceDefinition" {
			crd := &v1beta1.CustomResourceDefinition{}
			// Marshal Unstructured and Decode as CustomResourceDefinition. FromUnstructured has issues
			// converting JSON numbers to float64 for CRD minimum/maximum validation.
			cb, err := o.MarshalJSON()
			if err != nil {
				return err
			}
			dec := serializer.NewCodecFactory(Scheme).UniversalDeserializer()
			if _, _, err = dec.Decode(cb, nil, crd); err != nil {
				return fmt.Errorf("error decoding CRD: %v", err)
			}
			b.crds = append(b.crds, crd)
		}
	}

	b.cacheStale = false
	return nil
}
