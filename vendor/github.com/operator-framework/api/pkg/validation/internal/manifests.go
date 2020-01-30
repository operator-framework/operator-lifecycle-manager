package internal

import (
	"fmt"

	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"

	"github.com/blang/semver"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

const skipPackageAnnotationKey = "olm.skipRange"

var PackageUpdateGraphValidator interfaces.Validator = interfaces.ValidatorFunc(validatePackageUpdateGraphs)

func validatePackageUpdateGraphs(objs ...interface{}) (results []errors.ManifestResult) {
	var pkg *registry.PackageManifest
	bundles := []*registry.Bundle{}
	for _, obj := range objs {
		switch v := obj.(type) {
		case *registry.PackageManifest:
			if pkg == nil {
				pkg = v
			}
		case *registry.Bundle:
			bundles = append(bundles, v)
		}
	}
	if pkg != nil && len(bundles) > 0 {
		results = append(results, validatePackageUpdateGraph(pkg, bundles))
	}
	return results
}

func validatePackageUpdateGraph(pkg *registry.PackageManifest, bundles []*registry.Bundle) (result errors.ManifestResult) {
	// Collect all CSV names and ensure no duplicates. We will use these names
	// to check whether a spec.replaces references an existing CSV in bundles.
	csvNameMap := map[string]struct{}{}
	for _, bundle := range bundles {
		csv, err := bundle.ClusterServiceVersion()
		if err != nil {
			result.Add(errors.ErrInvalidParse("error getting bundle CSV", err))
			return result
		}
		if _, seenCSV := csvNameMap[csv.GetName()]; seenCSV {
			result.Add(errors.ErrInvalidCSV("duplicate CSV in bundle set", csv.GetName()))
			return result
		} else {
			csvNameMap[csv.GetName()] = struct{}{}
		}
	}

	// Check that all CSV fields follow package graph invariants.
	replacesGraph, replacesSet := map[string]string{}, map[string]struct{}{}
	for _, bundle := range bundles {
		// We already know each CSV in bundles can be marshalled correctly.
		csv, _ := bundle.ClusterServiceVersion()

		// spec.replaces, if present:
		// - Must be a valid Kubernetes resource name.
		// - Should reference the name of a CSV defined by another bundle.
		replaces, err := csv.GetReplaces()
		if err != nil {
			result.Add(errors.ErrInvalidParse("error getting spec.replaces from bundle CSV", err))
			return result
		}
		replacesGraph[csv.GetName()] = replaces
		if replaces != "" {
			if _, seen := replacesSet[replaces]; seen {
				result.Add(errors.ErrInvalidCSV(
					fmt.Sprintf("spec.replaces %q referenced by more than one CSV", replaces),
					csv.GetName()))
				return result
			}
			replacesSet[replaces] = struct{}{}

			if _, _, err = parseCSVNameFormat(replaces); err != nil {
				result.Add(errors.ErrInvalidCSV(fmt.Sprintf("spec.replaces %s", err), csv.GetName()))
			}
			if csv.GetName() == replaces {
				result.Add(errors.ErrInvalidCSV(
					"spec.replaces field cannot match its own metadata.name.", csv.GetName()))
			}
			if _, replacesInBundles := csvNameMap[replaces]; !replacesInBundles {
				result.Add(errors.WarnInvalidCSV(
					fmt.Sprintf("spec.replaces %q CSV is not present in manifests", replaces),
					csv.GetName()))
			}
		}

		// spec.skips, if present:
		// - Must contain valid Kubernetes resource names.
		// - Must not contain an element matching spec.replaces.
		skips, err := csv.GetSkips()
		if err != nil {
			result.Add(errors.ErrInvalidParse("error getting spec.skips from bundle CSV", err))
			return result
		}
		for i, skip := range skips {
			if _, _, err = parseCSVNameFormat(skip); err != nil {
				result.Add(errors.ErrInvalidCSV(
					fmt.Sprintf("spec.skips[%d] %s", i, err), csv.GetName()))
			}
			if skip == replaces && replaces != "" {
				result.Add(errors.ErrInvalidCSV(
					fmt.Sprintf("spec.skips[%d] %q cannot match spec.replaces", i, skip),
					csv.GetName()))
			}
		}

		// metadata.annotations["olm.skipRange"], if present:
		// - Must be a valid semver range.
		// - Must not be inclusive of its CSV’s version.
		skipRange, rerr := parseSkipRange(csv)
		if rerr != (errors.Error{}) {
			result.Add(rerr)
			return result
		}
		if skipRange != nil {
			csvVerStr, err := csv.GetVersion()
			if err != nil {
				result.Add(errors.ErrInvalidParse("error getting spec.version from bundle CSV", err))
			}
			csvVer, err := semver.Parse(csvVerStr)
			if err != nil {
				result.Add(errors.ErrInvalidParse("error parsing spec.version", err))
			}
			if skipRange(csvVer) {
				result.Add(errors.ErrInvalidCSV(
					fmt.Sprintf("metadata.annotations[\"%s\"] range contains the CSV's version",
						skipPackageAnnotationKey),
					csv.GetName()))
			}
		}
	}

	// Ensure no spec.replaces reference parent CSV's.
	result.Add(checkReplacesGraphForCycles(replacesGraph)...)
	// Ensure all channels reference existing CSV's.
	result.Add(checkChannelInBundle(pkg, replacesGraph)...)
	return result
}

func parseSkipRange(csv *registry.ClusterServiceVersion) (semver.Range, errors.Error) {
	if csv.GetAnnotations() != nil {
		if skipRangeStr, ok := csv.GetAnnotations()[skipPackageAnnotationKey]; ok {
			skipRange, err := semver.ParseRange(skipRangeStr)
			if err != nil {
				return nil, errors.ErrInvalidCSV(
					fmt.Sprintf("metadata.annotations[\"%s\"] %q is an invalid semantic version range",
						skipPackageAnnotationKey, skipRangeStr),
					csv.GetName())
			}
			return skipRange, errors.Error{}
		}
	}
	return nil, errors.Error{}
}

// checkReplacesGraphForCycles ensures no cycles occur in spec.replaces
// references. No spec.replaces should reference a parent CSV in the graph.
func checkReplacesGraphForCycles(graph map[string]string) (errs []errors.Error) {
	for csvName, replaces := range graph {
		currReplaces := replaces
		currCSVName := csvName
		for {
			newReplaces, ok := graph[currReplaces]
			if ok {
				if newReplaces == "" {
					break
				}
				if newReplaces == csvName {
					errs = append(errs, errors.ErrInvalidCSV(
						fmt.Sprintf("spec.replaces %q references a parent in CSV replace chain",
							newReplaces),
						currReplaces))
					break
				}
				currCSVName = currReplaces
				currReplaces = newReplaces
			} else {
				errs = append(errs, errors.ErrInvalidCSV(
					fmt.Sprintf("spec.replaces %q does not map to a CSV in bundles",
						currReplaces),
					currCSVName))
				break
			}
		}
	}
	return errs
}

// checkChannelInBundle ensures that each package channel's currentCSV exists
// in one bundle.
func checkChannelInBundle(pkg *registry.PackageManifest, csvNames map[string]string) (errs []errors.Error) {
	for _, channel := range pkg.Channels {
		if _, csvExists := csvNames[channel.CurrentCSVName]; !csvExists {
			errs = append(errs, errors.ErrInvalidPackageManifest(
				fmt.Sprintf("currentCSV %q for channel name %q not found in bundle",
					channel.CurrentCSVName, channel.Name),
				pkg.PackageName))
		}
	}
	return errs
}
