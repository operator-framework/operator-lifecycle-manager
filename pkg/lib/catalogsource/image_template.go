package catalogsource

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	versionutil "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apimachinery/pkg/version"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

const (

	// regex capture group names

	// capGrpKubeMajorV is a capture group name for a kube major version
	capGrpKubeMajorV = "kubemajorv"
	// capGrpKubeMinorV is a capture group name for a kube minor version
	capGrpKubeMinorV = "kubeminorv"
	// capGrpKubePatchV is a capture group name for a kube patch version
	capGrpKubePatchV = "kubepatchv"

	// static templates

	// TemplKubeMajorV is a template that represents the kube major version
	TemplKubeMajorV = "{kube_major_version}"
	// TemplKubeMinorV is a template that represents the kube minor version
	TemplKubeMinorV = "{kube_minor_version}"
	// TemplKubePatchV is a template that represents the kube patch version
	TemplKubePatchV = "{kube_patch_version}"

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

// templateMutex is a package scoped mutex for synchronizing access to templateNameToReplacementValuesMap
var templateMutex sync.RWMutex

func init() {
	// Handle known static templates
	initializeIfNeeded(TemplKubeMajorV)
	initializeIfNeeded(TemplKubeMinorV)
	initializeIfNeeded(TemplKubePatchV)
}

// initializeIfNeeded sets the map to a "missing" value if its not already present
func initializeIfNeeded(templateKey string) {
	templateMutex.Lock()
	defer templateMutex.Unlock()

	// have we encountered this template already?
	if _, ok := templateNameToReplacementValuesMap[templateKey]; !ok {
		// this is a new template, so default to missing value
		templateNameToReplacementValuesMap[templateKey] = templateMissing
	}
}

// resetMaps is only useful for test cases
func resetMaps() {
	templateMutex.Lock()
	templateNameToReplacementValuesMap = map[string]string{}
	templateMutex.Unlock()

	initializeIfNeeded(TemplKubeMajorV)
	initializeIfNeeded(TemplKubeMinorV)
	initializeIfNeeded(TemplKubePatchV)
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
	{capGrpKubeMajorV, TemplKubeMajorV},
	{capGrpKubeMinorV, TemplKubeMinorV},
	{capGrpKubePatchV, TemplKubePatchV},
}

var regexImageTemplates = regexp.MustCompile(regexList.String())

// ReplaceTemplates takes a catalog image reference containing templates (i.e. catalogImageTemplate)
// and attempts to replace the templates with actual values (if available).
// The return value processedCatalogImageTemplate represents the catalog image reference after processing.
// Callers of this function should check the unresolvedTemplates return value to determine
// if all values were properly resolved (i.e. empty slice means all items were resolved, and presence
// of a value in the slice means that the template was either not found in the cache or its value has not been
// fetched yet). Providing an empty catalogImageTemplate results in empty processedCatalogImageTemplate and
// zero length unresolvedTemplates
func ReplaceTemplates(catalogImageTemplate string) (processedCatalogImageTemplate string, unresolvedTemplates []string) {
	templateMutex.RLock()
	defer templateMutex.RUnlock()

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

func UpdateKubeVersion(serverVersion *version.Info, logger *logrus.Logger) {
	templateMutex.Lock()
	defer templateMutex.Unlock()

	if serverVersion == nil {
		logger.Warn("no server version provided")
		return
	}

	// need to use the gitversion from version.info.String() because minor version is not always Uint value
	// and patch version is not returned as a first class field
	semver, err := versionutil.ParseSemantic(serverVersion.String())
	if err != nil {
		logger.WithError(err).Error("unable to parse kube server version")
		return
	}

	templateNameToReplacementValuesMap[TemplKubeMajorV] = strconv.FormatUint(uint64(semver.Major()), 10)
	templateNameToReplacementValuesMap[TemplKubeMinorV] = strconv.FormatUint(uint64(semver.Minor()), 10)
	templateNameToReplacementValuesMap[TemplKubePatchV] = strconv.FormatUint(uint64(semver.Patch()), 10)
}
