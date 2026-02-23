package manifests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

// bundleLoader loads a bundle directory from disk
type packageManifestLoader struct {
	dir     string
	bundles []*Bundle
	pkg     *PackageManifest
}

func NewPackageManifestLoader(dir string) packageManifestLoader {
	return packageManifestLoader{
		dir: dir,
	}
}

func (p *packageManifestLoader) LoadPackage() error {
	errs := make([]error, 0)
	if err := filepath.Walk(p.dir, collectWalkErrs(p.LoadPackagesWalkFunc, &errs)); err != nil {
		errs = append(errs, err)
	}

	if err := filepath.Walk(p.dir, collectWalkErrs(p.LoadBundleWalkFunc, &errs)); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

// LoadPackagesWalkFunc attempts to unmarshal the file at the given path into a PackageManifest resource.
// If unmarshaling is successful, the PackageManifest is added to the loader's store.
func (p *packageManifestLoader) LoadPackagesWalkFunc(path string, f os.FileInfo, err error) error {
	if f == nil {
		return fmt.Errorf("invalid file: %v", f)
	}

	if f.IsDir() {
		if strings.HasPrefix(f.Name(), ".") {
			return filepath.SkipDir
		}
		return nil
	}

	if strings.HasPrefix(f.Name(), ".") {
		return nil
	}

	fileReader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to load package from file %s: %s", path, err)
	}
	defer fileReader.Close()

	decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)
	manifest := PackageManifest{}
	if err = decoder.Decode(&manifest); err != nil {
		if err != nil {
			return fmt.Errorf("could not decode contents of file %s into package: %s", path, err)
		}
	}

	if manifest.IsEmpty() {
		return nil
	}

	if p.pkg != nil {
		return fmt.Errorf("multiple package manifest files found in directory")
	}

	p.pkg = &manifest

	return nil
}

func (p *packageManifestLoader) LoadBundleWalkFunc(path string, f os.FileInfo, err error) error {
	if f == nil {
		return fmt.Errorf("invalid file: %v", f)
	}

	if f.IsDir() {
		if strings.HasPrefix(f.Name(), ".") {
			return filepath.SkipDir
		}
		return nil
	}

	if strings.HasPrefix(f.Name(), ".") {
		return nil
	}

	fileReader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to load file %s: %s", path, err)
	}
	defer fileReader.Close()

	decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)
	csv := unstructured.Unstructured{}

	if err = decoder.Decode(&csv); err != nil {
		return nil
	}

	if csv.GetKind() != operatorsv1alpha1.ClusterServiceVersionKind {
		return nil
	}

	var errs []error
	bundle, err := loadBundle(csv.GetName(), filepath.Dir(path))
	if err != nil {
		errs = append(errs, fmt.Errorf("error loading objs in directory: %s", err))
	}

	if bundle == nil || bundle.CSV == nil {
		errs = append(errs, fmt.Errorf("no bundle csv found"))
		return utilerrors.NewAggregate(errs)
	}

	p.bundles = append(p.bundles, bundle)

	return utilerrors.NewAggregate(errs)
}
