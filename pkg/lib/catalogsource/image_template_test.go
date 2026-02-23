package catalogsource

import (
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func TestImageTemplateRegex(t *testing.T) {
	var table = []struct {
		description string
		imageName   string
		isMatch     bool
		foundItems  []string
	}{
		{
			description: "no templates used",
			imageName:   "foo",
			isMatch:     false,
		},
		{
			description: "one static template used",
			imageName:   fmt.Sprintf("foo%s", TemplKubeMajorV),
			isMatch:     true,
			foundItems:  []string{TemplKubeMajorV},
		},
		{
			description: "multiple templates used",
			imageName:   fmt.Sprintf("%sfoo%s", TemplKubeMajorV, TemplKubeMinorV),
			isMatch:     true,
			foundItems:  []string{TemplKubeMajorV, TemplKubeMinorV},
		},
	}

	for _, tt := range table {
		t.Run(tt.description, func(t *testing.T) {

			assert.Equal(t, tt.isMatch, regexImageTemplates.MatchString(tt.imageName), "Expected image name %q to find a match", tt.imageName)

			actualFoundItems := regexImageTemplates.FindAllString(tt.imageName, -1)
			assert.Equal(t, tt.foundItems, actualFoundItems, "Expected to find all strings within image name %q", tt.imageName)
		})
	}
}

// TestImageTemplateFlow simulates the image template process.
func TestImageTemplateFlow(t *testing.T) {

	defaultServerVersion := version.Info{
		Major:      "1",
		Minor:      "20",
		GitVersion: "v1.20.0+bd9e442",
	}

	serverVersionNonReleaseBuild := version.Info{
		Major:      "1",
		Minor:      "20+",
		GitVersion: "v1.20.1+bd9e442",
	}

	var table = []struct {
		description                 string                 // description of test case
		catsrc                      v1alpha1.CatalogSource // fake catalog source for testing
		serverVersion               *version.Info          // simulated kube version information
		expectedCatalogTemplate     string                 // expected results after calling api
		expectedUnresolvedTemplates []string               // expected results after calling api
	}{
		{
			description:                 "no image templates",
			catsrc:                      v1alpha1.CatalogSource{},
			serverVersion:               &defaultServerVersion,
			expectedCatalogTemplate:     "",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "kube image template",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s", TemplKubeMajorV),
					},
				},
			},
			serverVersion:               &defaultServerVersion,
			expectedCatalogTemplate:     "foo/v1",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "multiple kube image template",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s.%s", TemplKubeMajorV, TemplKubeMinorV),
					},
				},
			},
			serverVersion:               &defaultServerVersion,
			expectedCatalogTemplate:     "foo/v1.20",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "kube image template but no server version",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s", TemplKubeMajorV),
					},
				},
			},
			serverVersion:               nil,
			expectedCatalogTemplate:     "foo/vmissing",
			expectedUnresolvedTemplates: []string{"{kube_major_version}"},
		},
		{
			description: "garbage image template",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s", "{foobar}"),
					},
				},
			},
			serverVersion:               &defaultServerVersion,
			expectedCatalogTemplate:     "foo/v{foobar}",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "multiple kube image template with patch and nonrelease build version",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s.%s.%s", TemplKubeMajorV, TemplKubeMinorV, TemplKubePatchV),
					},
				},
			},
			serverVersion:               &serverVersionNonReleaseBuild,
			expectedCatalogTemplate:     "foo/v1.20.1",
			expectedUnresolvedTemplates: []string{},
		},
	}

	logger := logrus.New()

	for _, tt := range table {
		t.Run(tt.description, func(t *testing.T) {
			// make sure test case is initialized to default to simulate real usage when operator starts up
			resetMaps()

			// simulate kube watch updates
			UpdateKubeVersion(tt.serverVersion, logger)
			// get the template
			catalogImageTemplate := GetCatalogTemplateAnnotation(&tt.catsrc)
			// perform the template replacement
			processedCatalogTemplate, unresolveTemplates := ReplaceTemplates(catalogImageTemplate)
			assert.Equal(t, tt.expectedCatalogTemplate, processedCatalogTemplate, "the processed template did not match expected value")
			assert.Equal(t, tt.expectedUnresolvedTemplates, unresolveTemplates, "the unresolved templates list did not match expected values")

		})
	}
}
