package manifests

// GetManifestsDir parses all bundles and a package manifest from a directory
func GetManifestsDir(dir string) (*PackageManifest, []*Bundle, error) {
	loader := NewPackageManifestLoader(dir)

	err := loader.LoadPackage()
	if err != nil {
		return nil, nil, err
	}

	return loader.pkg, loader.bundles, nil
}

// GetBundleFromDir takes a raw directory containg an Operator Bundle and
// serializes its component files (CSVs, CRDs, other native kube manifests)
// and returns it as a Bundle
func GetBundleFromDir(dir string) (*Bundle, error) {
	loader := NewBundleLoader(dir)

	err := loader.LoadBundle()
	if err != nil {
		return nil, err
	}

	return loader.bundle, nil
}
