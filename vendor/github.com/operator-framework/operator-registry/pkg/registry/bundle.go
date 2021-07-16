package registry

import (
	"encoding/json"
	"fmt"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	v1beta1CRDVersion = "v1beta1"
	v1CRDVersion      = "v1"
	CRDKind           = "CustomResourceDefinition"
)

// Scheme is the default instance of runtime.Scheme to which types in the Kubernetes API are already registered.
var Scheme = runtime.NewScheme()

// Codecs provides access to encoding and decoding for the scheme
var Codecs = serializer.NewCodecFactory(Scheme)

func DefaultYAMLDecoder() runtime.Decoder {
	return Codecs.UniversalDeserializer()
}

func init() {
	utilruntime.Must(apiextensionsv1beta1.AddToScheme(Scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(Scheme))
}

type Bundle struct {
	Name         string
	Objects      []*unstructured.Unstructured
	Package      string
	Channels     []string
	BundleImage  string
	version      string
	csv          *ClusterServiceVersion
	v1beta1crds  []*apiextensionsv1beta1.CustomResourceDefinition
	v1crds       []*apiextensionsv1.CustomResourceDefinition
	Dependencies []*Dependency
	Properties   []Property
	Annotations  *Annotations
	cacheStale   bool
}

func NewBundle(name string, annotations *Annotations, objs ...*unstructured.Unstructured) *Bundle {
	bundle := &Bundle{
		Name:        name,
		Package:     annotations.PackageName,
		Annotations: annotations,
	}
	for _, o := range objs {
		bundle.Add(o)
	}

	if annotations == nil {
		return bundle
	}
	bundle.Channels = strings.Split(annotations.Channels, ",")

	return bundle
}

func NewBundleFromStrings(name, version, pkg, defaultChannel, channels, objs string) (*Bundle, error) {
	objStrs, err := BundleStringToObjectStrings(objs)
	if err != nil {
		return nil, err
	}

	unstObjs := []*unstructured.Unstructured{}
	for _, o := range objStrs {
		dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(o), 10)
		unst := &unstructured.Unstructured{}
		if err := dec.Decode(unst); err != nil {
			return nil, err
		}
		unstObjs = append(unstObjs, unst)
	}

	annotations := &Annotations{
		PackageName:        pkg,
		Channels:           channels,
		DefaultChannelName: defaultChannel,
	}
	bundle := NewBundle(name, annotations, unstObjs...)
	bundle.version = version

	return bundle, nil
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
	if b.version != "" {
		return b.version, nil
	}

	var err error
	if err = b.cache(); err != nil {
		return "", err
	}

	if b.csv != nil {
		b.version, err = b.csv.GetVersion()
	}

	return b.version, err
}

func (b *Bundle) SkipRange() (string, error) {
	if err := b.cache(); err != nil {
		return "", err
	}
	return b.csv.GetSkipRange(), nil
}

func (b *Bundle) Replaces() (string, error) {
	if err := b.cache(); err != nil {
		return "", err
	}
	return b.csv.GetReplaces()
}

func (b *Bundle) Skips() ([]string, error) {
	if err := b.cache(); err != nil {
		return nil, err
	}
	return b.csv.GetSkips()
}

func (b *Bundle) Icons() ([]Icon, error) {
	if err := b.cache(); err != nil {
		return nil, err
	}
	return b.csv.GetIcons()
}

func (b *Bundle) Description() (string, error) {
	if err := b.cache(); err != nil {
		return "", err
	}
	return b.csv.GetDescription()
}

func (b *Bundle) CustomResourceDefinitions() ([]runtime.Object, error) {
	if err := b.cache(); err != nil {
		return nil, err
	}
	var crds []runtime.Object
	for _, crd := range b.v1crds {
		crds = append(crds, crd)
	}
	for _, crd := range b.v1beta1crds {
		crds = append(crds, crd)
	}
	return crds, nil
}

func (b *Bundle) ProvidedAPIs() (map[APIKey]struct{}, error) {
	provided := map[APIKey]struct{}{}
	crds, err := b.CustomResourceDefinitions()
	if err != nil {
		return nil, fmt.Errorf("error getting crds: %s", err)
	}

	for _, c := range crds {
		switch crd := c.(type) {
		case *apiextensionsv1.CustomResourceDefinition:
			for _, v := range crd.Spec.Versions {
				provided[APIKey{Group: crd.Spec.Group, Version: v.Name, Kind: crd.Spec.Names.Kind, Plural: crd.Spec.Names.Plural}] = struct{}{}
			}
		case *apiextensionsv1beta1.CustomResourceDefinition:
			for _, v := range crd.Spec.Versions {
				provided[APIKey{Group: crd.Spec.Group, Version: v.Name, Kind: crd.Spec.Names.Kind, Plural: crd.Spec.Names.Plural}] = struct{}{}
			}
			if crd.Spec.Version != "" {
				provided[APIKey{Group: crd.Spec.Group, Version: crd.Spec.Version, Kind: crd.Spec.Names.Kind, Plural: crd.Spec.Names.Plural}] = struct{}{}
			}
		default:
			return nil, fmt.Errorf("unknown api version in crd: %#v", crd)
		}
	}

	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return nil, err
	}

	ownedAPIs, _, err := csv.GetApiServiceDefinitions()
	if err != nil {
		return nil, fmt.Errorf("error getting apiservice definitions: %s", err)
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

func (b *Bundle) Serialize() (csvName, bundleImage string, csvBytes []byte, bundleBytes []byte, annotationBytes []byte, err error) {
	csvCount := 0
	for _, obj := range b.Objects {
		objBytes, err := runtime.Encode(unstructured.UnstructuredJSONScheme, obj)
		if err != nil {
			return "", "", nil, nil, nil, err
		}
		bundleBytes = append(bundleBytes, objBytes...)

		if obj.GroupVersionKind().Kind == "ClusterServiceVersion" {
			csvName = obj.GetName()
			csvBytes, err = runtime.Encode(unstructured.UnstructuredJSONScheme, obj)
			if err != nil {
				return "", "", nil, nil, nil, err
			}
			csvCount += 1
			if csvCount > 1 {
				return "", "", nil, nil, nil, fmt.Errorf("two csvs found in one bundle")
			}
		}
	}

	if b.Annotations != nil {
		annotationBytes, err = json.Marshal(b.Annotations)
	}

	return csvName, b.BundleImage, csvBytes, bundleBytes, annotationBytes, nil
}

func (b *Bundle) Images() (map[string]struct{}, error) {
	result := make(map[string]struct{})

	if b.BundleImage != "" {
		result[b.BundleImage] = struct{}{}
	}

	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return nil, err
	}

	if csv == nil {
		return result, nil
	}

	images, err := csv.GetOperatorImages()
	if err != nil {
		return nil, err
	}
	for img := range images {
		result[img] = struct{}{}
	}

	relatedImages, err := csv.GetRelatedImages()
	if err != nil {
		return nil, err
	}
	for img := range relatedImages {
		result[img] = struct{}{}
	}

	return result, nil
}

func (b *Bundle) cache() error {
	if !b.cacheStale {
		return nil
	}
	for _, o := range b.Objects {
		if o.GroupVersionKind().Kind == "ClusterServiceVersion" {
			csv := &ClusterServiceVersion{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.UnstructuredContent(), csv); err != nil {
				return err
			}
			b.csv = csv
			break
		}
	}

	for _, o := range b.Objects {
		if o.GroupVersionKind().Kind == CRDKind {
			// Marshal Unstructured and Decode as CustomResourceDefinition. FromUnstructured has issues
			// converting JSON numbers to float64 for CRD minimum/maximum validation.
			cb, err := o.MarshalJSON()
			if err != nil {
				return err
			}
			dec := serializer.NewCodecFactory(Scheme).UniversalDeserializer()
			switch o.GroupVersionKind().Version {
			case v1CRDVersion:
				crd := &apiextensionsv1.CustomResourceDefinition{}
				if _, _, err = dec.Decode(cb, nil, crd); err != nil {
					return fmt.Errorf("error decoding v1 CRD: %v", err)
				}
				b.v1crds = append(b.v1crds, crd)
			case v1beta1CRDVersion:
				crd := &apiextensionsv1beta1.CustomResourceDefinition{}
				if _, _, err = dec.Decode(cb, nil, crd); err != nil {
					return fmt.Errorf("error decoding v1beta1 CRD: %v", err)
				}
				b.v1beta1crds = append(b.v1beta1crds, crd)
			}
		}
	}

	b.cacheStale = false
	return nil
}

func (b *Bundle) SubstitutesFor() (string, error) {
	if err := b.cache(); err != nil {
		return "", err
	}
	return b.csv.GetSubstitutesFor(), nil
}
