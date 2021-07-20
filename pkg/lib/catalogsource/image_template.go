package catalogsource

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/blang/semver/v4"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/util/jsonpath"
)

const (

	// regex capture group names

	// cap_grp_kubemajorv is a capture group name for a kube major version
	cap_grp_kubemajorv = "kubemajorv"
	// cap_grp_kubeminorv is a capture group name for a kube minor version
	cap_grp_kubeminorv = "kubeminorv"
	// cap_grp_kubepatchv is a capture group name for a kube patch version
	cap_grp_kubepatchv = "kubepatchv"
	// cap_grp_gvk is a capture group name for a dynamic template that uses its own subgroups
	cap_grp_gvk = "gvk"

	// cap_subgrp_group is a sub capture group name used in a dynamic template
	cap_subgrp_group = "group"
	// cap_subgrp_version is a sub capture group name used in a dynamic template
	cap_subgrp_version = "version"
	// cap_subgrp_kind is a sub capture group name used in a dynamic template
	cap_subgrp_kind = "kind"
	// cap_subgrp_name is a sub capture group name used in a dynamic template
	cap_subgrp_name = "name"
	// cap_subgrp_namespace is a sub capture group name used in a dynamic template
	cap_subgrp_namespace = "namespace"
	// cap_subgrp_jsonpath is a sub capture group name used in a dynamic template
	cap_subgrp_jsonpath = "jsonpath"

	// static templates

	// TEMPL_KUBEMAJORV is a template that represents the kube major version
	TEMPL_KUBEMAJORV = "{kube_major_version}"
	// TEMPL_KUBEMINORV is a template that represents the kube minor version
	TEMPL_KUBEMINORV = "{kube_minor_version}"
	// TEMPL_KUBEPATCHV is a template that represents the kube patch version
	TEMPL_KUBEPATCHV = "{kube_patch_version}"

	// templ_gvk is a dynamic template that uses its own subgroups
	templ_gvk = "{group:(?P<group>.*?),version:(?P<version>.*?),kind:(?P<kind>.*?),name:(?P<name>.*?),namespace:(?P<namespace>.*?),jsonpath:(?P<jsonpath>{.*?})}"

	// templateMissing represents a value that could not be obtained from the cluster
	templateMissing = "missing"

	// catalogImageTemplateAnnotation is OLM annotation. The annotation value that corresponds
	// to this key is used as means to adjust a catalog source image, where templated
	// values are replaced with values found in the cluster
	CatalogImageTemplateAnnotation = "olm.catalogImageTemplate"
)

// templateNameToReplacementValuesMap is storage for templates and their resolved values
// The values are initialized to variable "templateMissing"
var templateNameToReplacementValuesMap = map[string]string{}

// convertToKey is a function that creates a key for templateNameToReplacementValuesMap based on a GVK key and json path
func convertToKey(key GVK_Key, jsonPath string) string {
	return fmt.Sprintf("{group:%s,version:%s,kind:%s,name:%s,namespace:%s,jsonpath:%s}", key.Group, key.Version, key.Kind, key.name, key.namespace, jsonPath)
}

// gvkToJSONPathMap is a multimap (i.e. one key with multiple values) where each value is
// zero or more JSON paths. In other words the user could specify multiple JSON path references
// for the same kubernetes manifest
var gvkToJSONPathMap = map[GVK_Key][]string{}

func init() {
	// Handle known static templates
	initializeIfNeeded(TEMPL_KUBEMAJORV)
	initializeIfNeeded(TEMPL_KUBEMINORV)
	initializeIfNeeded(TEMPL_KUBEPATCHV)
}

// initializeIfNeeded sets the map to a "missing" value if its not already present
func initializeIfNeeded(templateKey string) {
	// have we encountered this template already?
	if _, ok := templateNameToReplacementValuesMap[templateKey]; !ok {
		// this is a new template, so default to missing value
		templateNameToReplacementValuesMap[templateKey] = templateMissing
	}
}

// resetMaps is only useful for test cases
func resetMaps() {
	templateNameToReplacementValuesMap = map[string]string{}
	gvkToJSONPathMap = map[GVK_Key][]string{}
	initializeIfNeeded(TEMPL_KUBEMAJORV)
	initializeIfNeeded(TEMPL_KUBEMINORV)
	initializeIfNeeded(TEMPL_KUBEPATCHV)
}

type regexEntry struct {
	captureGroup string
	template     string
}

func (r *regexEntry) String() string {
	return fmt.Sprintf(`(?P<%s>%s)`, r.captureGroup, r.template)
}

type regexEntries []regexEntry

func (r regexEntries) String() string {
	regexEntryAsString := []string{}
	for _, regexEntry := range r {
		regexEntryAsString = append(regexEntryAsString, regexEntry.String())
	}
	result := strings.Join(regexEntryAsString, "|")
	return result
}

var regexList = regexEntries{
	{cap_grp_kubemajorv, TEMPL_KUBEMAJORV},
	{cap_grp_kubeminorv, TEMPL_KUBEMINORV},
	{cap_grp_kubepatchv, TEMPL_KUBEPATCHV},
	{cap_grp_gvk, templ_gvk},
}

var regexImageTemplates = regexp.MustCompile(regexList.String())

var regexGVKTemplate = regexp.MustCompile(templ_gvk)

// ReplaceTemplates takes a catalog image reference containing templates (i.e. catalogImageTemplate)
// and attempts to replace the templates with actual values (if available).
// The return value processedCatalogImageTemplate represents the catalog image reference after processing.
// Callers of this function should check the unresolvedTemplates return value to determine
// if all values were properly resolved (i.e. empty slice means all items were resolved, and presence
// of a value in the slice means that the template was either not found in the cache or its value has not been
// fetched yet). Providing an empty catalogImageTemplate results in empty processedCatalogImageTemplate and
// zero length unresolvedTemplates
func ReplaceTemplates(catalogImageTemplate string) (processedCatalogImageTemplate string, unresolvedTemplates []string) {

	// init to empty slice
	unresolvedTemplates = []string{}

	// templateReplacer function determines the replacement value for the given template
	var templateReplacer = func(template string) string {
		replacement, ok := templateNameToReplacementValuesMap[template]
		if ok {
			// found a template, but check if the value is missing and keep record of
			// what templates were unresolved
			if replacement == templateMissing {
				unresolvedTemplates = append(unresolvedTemplates, template)
			}
			return replacement
		} else {
			// probably should not get here, but in case there is no match,
			// just return template unchanged, but keep record of
			// what templates were unresolved
			unresolvedTemplates = append(unresolvedTemplates, template)
			return template
		}
	}

	// if image is present, perform template substitution
	if catalogImageTemplate != "" {
		processedCatalogImageTemplate = regexImageTemplates.ReplaceAllStringFunc(catalogImageTemplate, templateReplacer)
	}
	return
}

// GetCatalogTemplateAnnotation is a helper function to extract the catalog image template annotation.
// Returns empty string if value not set, otherwise returns annotation.
func GetCatalogTemplateAnnotation(catalogSource *v1alpha1.CatalogSource) string {
	if catalogSource == nil {
		return ""
	}
	if catalogImageTemplate, ok := catalogSource.GetAnnotations()[CatalogImageTemplateAnnotation]; !ok {
		return ""
	} else {
		return catalogImageTemplate
	}
}

// GVK_Key uniquely represents a Group/Version/Kind (with optional name/namespace)
// and can be used as a key for retrieval of data associated with this key
type GVK_Key struct {
	schema.GroupVersionKind
	name      string
	namespace string
}

func InitializeCatalogSourceTemplates(catalogSource *v1alpha1.CatalogSource) []GVK_Key {

	// capture a list of keys that were found in this catalog source
	foundGVKs := []GVK_Key{}

	// findNamedMatches will return a map whose key is the named capture group, and value is the value of the capture group
	findNamedMatches := func(str string) map[string]string {
		// Note: matches and names indices are "in sync"
		matches := regexGVKTemplate.FindStringSubmatch(str)
		names := regexGVKTemplate.SubexpNames()

		results := map[string]string{}
		for i, match := range matches {
			// only add named groups to the map
			if names[i] != "" {
				results[names[i]] = match
			}
		}
		return results
	}

	catalogImageTemplate := GetCatalogTemplateAnnotation(catalogSource)
	if catalogImageTemplate != "" {
		/* Handle GVK templates */

		// get every GVK template available (if any)
		gvkTemplates := regexGVKTemplate.FindAllString(catalogImageTemplate, -1)
		// add each GVK template for later use, initializing to missing value
		for _, gvkTemplate := range gvkTemplates {
			initializeIfNeeded(gvkTemplate)

			// get the sub groups
			subGroupMap := findNamedMatches(gvkTemplate)

			// create GVKTemplate to use as a key... add values from the subgroups as best we can, defaults to empty string for values not found
			key := GVK_Key{
				GroupVersionKind: schema.GroupVersionKind{Group: subGroupMap[cap_subgrp_group], Version: subGroupMap[cap_subgrp_version], Kind: subGroupMap[cap_subgrp_kind]},
				name:             subGroupMap[cap_subgrp_name],
				namespace:        subGroupMap[cap_subgrp_namespace],
			}
			jsonPath := subGroupMap[cap_subgrp_jsonpath]

			// see if we've already added this key (don't add duplicates)
			gvkPresent := false
			for _, existingGVK := range foundGVKs {
				if reflect.DeepEqual(existingGVK, key) {
					gvkPresent = true
				}
			}
			if !gvkPresent {
				foundGVKs = append(foundGVKs, key)
			}

			// add this entry to the map and append the jsonPath to the array
			if existingJsonPaths, ok := gvkToJSONPathMap[key]; ok {
				// map already has this key, now find out if we've already added this path
				foundEntry := false
				for _, existingJsonPath := range existingJsonPaths {
					if jsonPath == existingJsonPath {
						foundEntry = true
					}
				}
				// if we did not find a jsonpath entry then add it now
				if !foundEntry {
					gvkToJSONPathMap[key] = append(gvkToJSONPathMap[key], jsonPath)
				}
			} else {
				gvkToJSONPathMap[key] = append(gvkToJSONPathMap[key], jsonPath)
			}

		}
	}
	return foundGVKs
}

func UpdateGVKValue(u *unstructured.Unstructured, logger *logrus.Logger) {

	// reconstitute the key
	key := GVK_Key{
		GroupVersionKind: u.GetObjectKind().GroupVersionKind(),
		name:             u.GetName(),
		namespace:        u.GetNamespace(),
	}

	// find the JSON paths
	if jsonPaths, ok := gvkToJSONPathMap[key]; ok {

		// convert the unstructured object into JSON as bytes
		jsonBytes, err := u.MarshalJSON()
		if err != nil {
			logger.WithError(err).Warn("unable to convert kube manifest to JSON")
		}

		// pass the JSON as bytes through the go json library so its in the right format
		var processedJSON interface{}
		err = json.Unmarshal(jsonBytes, &processedJSON)
		if err != nil {
			logger.WithError(err).Warn("unable to convert kube manifest json data into usable form")
		}

		for _, jsonPath := range jsonPaths {

			gvkLogger := logger.WithFields(logrus.Fields{
				"gvk":      u.GetObjectKind().GroupVersionKind().String(),
				"jsonPath": jsonPath,
			})
			// create the json path parser
			jsonPathParser := jsonpath.New("GVK path parser")

			// parse the json path template
			err = jsonPathParser.Parse(jsonPath)
			if err != nil {
				gvkLogger.WithError(err).Warn("unable to parse json path template")
				continue
			}

			// execute the parser using the JSON data writing the results into a buffer
			buf := new(bytes.Buffer)
			err = jsonPathParser.Execute(buf, processedJSON)
			if err != nil {
				gvkLogger.WithError(err).Warn("unable to execute json path parsing")
				continue
			}

			templateMapKey := convertToKey(key, jsonPath)
			templateMapValue := buf.String()

			if jsonPath == templateMapValue {
				// the jsonpath is exactly the same as the templateMapValue, this means
				// that the jsonpath was probably invalid, so don't update
				gvkLogger.Debugf("jsonpath %q is likely invalid (maybe curly braces are missing?)", jsonPath)
				continue
			}
			// reconstruct the key for the template replacement map and add
			// whatever we got from the json path execution
			templateNameToReplacementValuesMap[templateMapKey] = templateMapValue
			gvkLogger.Debugf("updated templateNameToReplacementValuesMap: key=%q value%q", templateMapKey, templateMapValue)
		}
	}
}

func UpdateKubeVersion(serverVersion *version.Info, logger *logrus.Logger) {
	if serverVersion == nil {
		logger.Warn("no server version provided")
		return
	}

	templateNameToReplacementValuesMap[TEMPL_KUBEMAJORV] = serverVersion.Major
	templateNameToReplacementValuesMap[TEMPL_KUBEMINORV] = serverVersion.Minor

	// api server does not explicitly give patch value, so we have to resort to parsing the git version
	serverGitVersion, err := semver.Parse(serverVersion.GitVersion)
	if err != nil {
		templateNameToReplacementValuesMap[TEMPL_KUBEPATCHV] = strconv.FormatUint(serverGitVersion.Patch, 10)
	} else {
		logger.WithError(err).Warn("unable to obtain kube server patch value")
	}
}
