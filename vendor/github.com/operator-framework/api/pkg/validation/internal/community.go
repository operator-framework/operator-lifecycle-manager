package internal

import (
	"encoding/json"
	"fmt"
	"github.com/blang/semver"
	"io/ioutil"
	"os"
	"strings"

	"github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

// IndexImagePathKey defines the key which can be used by its consumers
// to inform where their index image path is to be checked
const IndexImagePathKey = "index-path"

// ocpLabelindex defines the OCP label which allow configure the OCP versions
// where the bundle will be distributed
const ocpLabelindex = "com.redhat.openshift.versions"

// CommunityOperatorValidator validates the bundle manifests against the required criteria to publish
// the projects on the community operators
//
// Note that this validator allows to receive a List of optional values as key=values. Currently, only the
// `index-path` key is allowed. If informed, it will check the labels on the image index according to its criteria.
var CommunityOperatorValidator interfaces.Validator = interfaces.ValidatorFunc(communityValidator)

func communityValidator(objs ...interface{}) (results []errors.ManifestResult) {

	// Obtain the k8s version if informed via the objects an optional
	var indexImagePath = ""
	for _, obj := range objs {
		switch obj.(type) {
		case map[string]string:
			indexImagePath = obj.(map[string]string)[IndexImagePathKey]
			if len(indexImagePath) > 0 {
				break
			}
		}
	}

	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.Bundle:
			results = append(results, validateCommunityBundle(v, indexImagePath))
		}
	}

	return results
}

type CommunityOperatorChecks struct {
	bundle         manifests.Bundle
	indexImagePath string
	errs           []error
	warns          []error
}

// validateCommunityBundle will check the bundle against the community-operator criterias
func validateCommunityBundle(bundle *manifests.Bundle, indexImagePath string) errors.ManifestResult {
	result := errors.ManifestResult{Name: bundle.Name}
	if bundle == nil {
		result.Add(errors.ErrInvalidBundle("Bundle is nil", nil))
		return result
	}

	if bundle.CSV == nil {
		result.Add(errors.ErrInvalidBundle("Bundle csv is nil", bundle.Name))
		return result
	}

	checks := CommunityOperatorChecks{bundle: *bundle, indexImagePath: indexImagePath, errs: []error{}, warns: []error{}}

	deprecatedAPIs := getRemovedAPIsOn1_22From(bundle)
	// Check if has deprecated apis then, check the olm.maxOpenShiftVersion property
	if len(deprecatedAPIs) > 0 {
		deprecatedAPIsMessage := generateMessageWithDeprecatedAPIs(deprecatedAPIs)
		checks = checkMaxOpenShiftVersion(checks, deprecatedAPIsMessage)
		checks = checkOCPLabelsWithHasDeprecatedAPIs(checks, deprecatedAPIsMessage)
		for _, err := range checks.errs {
			result.Add(errors.ErrInvalidCSV(err.Error(), bundle.CSV.GetName()))
		}
		for _, warn := range checks.warns {
			result.Add(errors.WarnInvalidCSV(warn.Error(), bundle.CSV.GetName()))
		}
	}

	return result
}

type propertiesAnnotation struct {
	Type  string
	Value string
}

// checkMaxOpenShiftVersion will verify if the OpenShiftVersion property was informed
func checkMaxOpenShiftVersion(checks CommunityOperatorChecks, v1beta1MsgForResourcesFound string) CommunityOperatorChecks {
	// Ensure that has the OCPMaxAnnotation
	const olmproperties = "olm.properties"
	const olmmaxOpenShiftVersion = "olm.maxOpenShiftVersion"
	semVerOCPV1beta1Unsupported, _ := semver.ParseTolerant(ocpVerV1beta1Unsupported)

	properties := checks.bundle.CSV.Annotations[olmproperties]
	if len(properties) == 0 {
		checks.errs = append(checks.errs, fmt.Errorf("csv.Annotations not specified %s for an "+
			"OCP version < %s. This annotation is required to prevent the user from upgrading their OCP cluster "+
			"before they have installed a version of their operator which is compatible with %s. This bundle is using APIs which were deprecated and removed in v1.22. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22 which are no "+
			"longer supported on %s. Migrate the API(s) for %s or use the annotation",
			olmmaxOpenShiftVersion,
			ocpVerV1beta1Unsupported,
			ocpVerV1beta1Unsupported,
			ocpVerV1beta1Unsupported,
			v1beta1MsgForResourcesFound))
		return checks
	}

	var properList []propertiesAnnotation
	if err := json.Unmarshal([]byte(properties), &properList); err != nil {
		checks.errs = append(checks.errs, fmt.Errorf("csv.Annotations has an invalid value specified for %s. "+
			"Please, check the value  (%s) and ensure that it is an array such as: "+
			"\"olm.properties\": '[{\"type\": \"key name\", \"value\": \"key value\"}]'",
			olmproperties, properties))
		return checks
	}

	hasOlmMaxOpenShiftVersion := false
	olmMaxOpenShiftVersionValue := ""
	for _, v := range properList {
		if v.Type == olmmaxOpenShiftVersion {
			hasOlmMaxOpenShiftVersion = true
			olmMaxOpenShiftVersionValue = v.Value
			break
		}
	}

	if !hasOlmMaxOpenShiftVersion {
		checks.errs = append(checks.errs, fmt.Errorf("csv.Annotations.%s with the "+
			"key `%s` and a value with an OCP version which is < %s is required for any operator "+
			"bundle that is using APIs which were deprecated and removed in v1.22. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22. Migrate the API(s) for %s or use the annotation",
			olmproperties,
			olmmaxOpenShiftVersion,
			ocpVerV1beta1Unsupported,
			v1beta1MsgForResourcesFound))
		return checks
	}

	semVerVersionMaxOcp, err := semver.ParseTolerant(olmMaxOpenShiftVersionValue)
	if err != nil {
		checks.errs = append(checks.errs, fmt.Errorf("csv.Annotations.%s has an invalid value."+
			"Unable to parse (%s) using semver : %s",
			olmproperties, olmMaxOpenShiftVersionValue, err))
		return checks
	}

	if semVerVersionMaxOcp.GE(semVerOCPV1beta1Unsupported) {
		checks.errs = append(checks.errs, fmt.Errorf("csv.Annotations.%s with the "+
			"key and value for %s has the OCP version value %s which is >= of %s. This bundle is using APIs which were deprecated and removed in v1.22. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22. "+
			"Migrate the API(s) for %s "+
			"or inform in this property an OCP version which is < %s",
			olmproperties,
			olmmaxOpenShiftVersion,
			olmMaxOpenShiftVersionValue,
			ocpVerV1beta1Unsupported,
			v1beta1MsgForResourcesFound,
			ocpVerV1beta1Unsupported))
		return checks
	}

	return checks
}

// checkOCPLabels will ensure that OCP labels are set and with a ocp target < 4.9
func checkOCPLabelsWithHasDeprecatedAPIs(checks CommunityOperatorChecks, deprecatedAPImsg string) CommunityOperatorChecks {
	// Note that we cannot make mandatory because the package format still valid
	if len(checks.indexImagePath) == 0 {
		checks.warns = append(checks.errs, fmt.Errorf("please, inform the path of "+
			"its index image file via the the optional key values and the key %s to allow this validator check the labels "+
			"configuration or migrate the API(s) for %s. "+
			"(e.g. %s=./mypath/bundle.Dockerfile). This bundle is using APIs which were deprecated and removed in v1.22. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22 ",
			IndexImagePathKey,
			deprecatedAPImsg,
			IndexImagePathKey))
		return checks
	}

	return validateImageFile(checks, deprecatedAPImsg)
}

func validateImageFile(checks CommunityOperatorChecks, deprecatedAPImsg string) CommunityOperatorChecks {
	if len(checks.indexImagePath) == 0 {
		return checks
	}

	info, err := os.Stat(checks.indexImagePath)
	if err != nil {
		checks.errs = append(checks.errs, fmt.Errorf("the index image in the path "+
			"(%s) was not found. Please, inform the path of the bundle operator index image via the the optional key values and the key %s. "+
			"(e.g. %s=./mypath/bundle.Dockerfile). Error : %s", checks.indexImagePath, IndexImagePathKey, IndexImagePathKey, err))
		return checks
	}
	if info.IsDir() {
		checks.errs = append(checks.errs, fmt.Errorf("the index image in the path "+
			"(%s) is not file. Please, inform the path of its index image via the the optional key values and the key %s. "+
			"(e.g. %s=./mypath/bundle.Dockerfile). The value informed is a diretory and not a file", checks.indexImagePath, IndexImagePathKey, IndexImagePathKey))
		return checks
	}

	b, err := ioutil.ReadFile(checks.indexImagePath)
	if err != nil {
		checks.errs = append(checks.errs, fmt.Errorf("unable to read the index image in the path "+
			"(%s). Error : %s", checks.indexImagePath, err))
		return checks
	}

	indexPathContent := string(b)
	hasOCPLabel := strings.Contains(indexPathContent, ocpLabelindex)
	if hasOCPLabel {
		semVerOCPV1beta1Unsupported, _ := semver.ParseTolerant(ocpVerV1beta1Unsupported)
		// the OCP range informed cannot allow carry on to OCP 4.9+
		line := strings.Split(indexPathContent, "\n")
		for i := 0; i < len(line); i++ {
			if strings.Contains(line[i], ocpLabelindex) {
				if !strings.Contains(line[i], "=") {
					checks.errs = append(checks.errs, fmt.Errorf("invalid syntax (%s) for (%s)",
						line[i],
						ocpLabelindex))
					return checks
				}

				value := strings.Split(line[i], "=")
				indexRange := value[1]
				doubleCote := "\""
				singleCote := "'"
				indexRange = strings.ReplaceAll(indexRange, singleCote, "")
				indexRange = strings.ReplaceAll(indexRange, doubleCote, "")
				if len(indexRange) > 1 {
					// if has the = then, the value needs to be < 4.9
					if strings.Contains(indexRange, "=") {
						version := strings.Split(indexRange, "=")[1]
						verParsed, err := semver.ParseTolerant(version)
						if err != nil {
							checks.errs = append(checks.errs, fmt.Errorf("unable to parse the value (%s) on (%s)",
								version, ocpLabelindex))
							return checks
						}

						if verParsed.GE(semVerOCPV1beta1Unsupported) {
							checks.errs = append(checks.errs, fmt.Errorf("this bundle is using APIs which were " +
								"deprecated and removed in v1.22. " +
								"More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22. " +
								"Migrate the API(s) for "+
								"%s or provide compatible version(s) by using the %s annotation in " +
								"`metadata/annotations.yaml` to ensure that the index image will be geneared " +
								"with its label. (e.g. LABEL %s='4.6-4.8')",
								deprecatedAPImsg,
								ocpLabelindex,
								ocpLabelindex))
							return checks
						}
					} else {
						// if not has not the = then the value needs contains - value less < 4.9
						if !strings.Contains(indexRange, "-") {
							checks.errs = append(checks.errs, fmt.Errorf("this bundle is using APIs which were " +
								"deprecated and removed in v1.22. " +
								"More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22 "+
								"The %s allows to distribute it on >= %s. Migrate the API(s) for "+
								"%s or provide compatible version(s) by using the %s annotation in " +
								"`metadata/annotations.yaml` to ensure that the index image will be generated " +
								"with its label. (e.g. LABEL %s='4.6-4.8')",
								indexRange,
								ocpVerV1beta1Unsupported,
								deprecatedAPImsg,
								ocpLabelindex,
								ocpLabelindex))
							return checks
						}

						version := strings.Split(indexRange, "-")[1]
						verParsed, err := semver.ParseTolerant(version)
						if err != nil {
							checks.errs = append(checks.errs, fmt.Errorf("unable to parse the value (%s) on (%s)",
								version, ocpLabelindex))
							return checks
						}

						if verParsed.GE(semVerOCPV1beta1Unsupported) {
							checks.errs = append(checks.errs, fmt.Errorf("this bundle is using APIs which were " +
								"deprecated and removed in v1.22. " +
								"More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22. " +
								"Upgrade the APIs from "+
								"for %s or provide compatible distribution version(s) by using the %s " +
								"annotation in `metadata/annotations.yaml` to ensure that the index image will " +
								"be generated with its label. (e.g. LABEL %s='4.6-4.8')",
								deprecatedAPImsg,
								ocpLabelindex,
								ocpLabelindex))
							return checks
						}

					}
				} else {
					checks.errs = append(checks.errs, fmt.Errorf("unable to get the range informed on %s",
						ocpLabelindex))
					return checks
				}
				break
			}
		}
	} else {
		checks.errs = append(checks.errs, fmt.Errorf("this bundle is using APIs which were deprecated and " +
			"removed in v1.22. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22. " +
			"Migrate the APIs "+
			"for %s or provide compatible version(s) via the labels. (e.g. LABEL %s='4.6-4.8')",
			deprecatedAPImsg,
			ocpLabelindex))
		return checks
	}
	return checks
}
