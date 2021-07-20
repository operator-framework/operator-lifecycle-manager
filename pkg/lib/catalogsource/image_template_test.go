package catalogsource

import (
	"fmt"
	"testing"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
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
			imageName:   fmt.Sprintf("foo%s", TEMPL_KUBEMAJORV),
			isMatch:     true,
			foundItems:  []string{TEMPL_KUBEMAJORV},
		},
		{
			description: "one gvk template used",
			imageName:   fmt.Sprintf("foo%s", "{group:foo.example.com,version:v1,kind:Sample,name:MySample,namespace:ns,jsonpath:{.spec.foo.bar}}"),
			isMatch:     true,
			foundItems:  []string{"{group:foo.example.com,version:v1,kind:Sample,name:MySample,namespace:ns,jsonpath:{.spec.foo.bar}}"},
		},
		{
			description: "multiple templates used",
			imageName:   fmt.Sprintf("%sfoo%s", TEMPL_KUBEMAJORV, "{group:foo.example.com,version:v1,kind:Sample,name:MySample,namespace:ns,jsonpath:{.spec.foo.bar}}"),
			isMatch:     true,
			foundItems:  []string{TEMPL_KUBEMAJORV, "{group:foo.example.com,version:v1,kind:Sample,name:MySample,namespace:ns,jsonpath:{.spec.foo.bar}}"},
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

func TestImageTemplateGVKRegex(t *testing.T) {

	exepctedNames := []string{"", cap_subgrp_group, cap_subgrp_version, cap_subgrp_kind, cap_subgrp_name, cap_subgrp_namespace, cap_subgrp_jsonpath}

	var table = []struct {
		description string
		gvkTemplate string
		matches     []string
		names       []string
	}{
		{
			description: "no gvk template used",
			gvkTemplate: "foo",
			names:       exepctedNames,
		},
		{
			description: "gvk template with all values",
			gvkTemplate: "{group:foo.example.com,version:v1,kind:Sample,name:MySample,namespace:ns,jsonpath:{.spec.foo.bar}}",
			matches:     []string{"{group:foo.example.com,version:v1,kind:Sample,name:MySample,namespace:ns,jsonpath:{.spec.foo.bar}}", "foo.example.com", "v1", "Sample", "MySample", "ns", "{.spec.foo.bar}"},
			names:       exepctedNames,
		},
		{
			description: "gvk template with no values but jsonpath must be present",
			gvkTemplate: "{group:,version:,kind:,name:,namespace:,jsonpath:{}}",
			matches:     []string{"{group:,version:,kind:,name:,namespace:,jsonpath:{}}", "", "", "", "", "", "{}"},
			names:       exepctedNames,
		},
		{
			description: "gvk template with no values but jsonpath is missing",
			gvkTemplate: "{group:,version:,kind:,name:,namespace:,jsonpath:}",
			matches:     nil,
			names:       exepctedNames,
		},
	}

	for _, tt := range table {
		t.Run(tt.description, func(t *testing.T) {

			matches := regexGVKTemplate.FindStringSubmatch(tt.gvkTemplate)
			names := regexGVKTemplate.SubexpNames()
			assert.Equal(t, tt.matches, matches, "Expected matches to equal")
			assert.Equal(t, tt.names, names, "Expected names to equal")
		})
	}
}

// TestImageTemplateFlow simulates the image template process. GVK templates
// use a jsonpath pointing to fake annotations to obtain their values
func TestImageTemplateFlow(t *testing.T) {

	defaultServerVersion := version.Info{
		Major:      "1",
		Minor:      "20",
		GitVersion: "v1.20.0+bd9e442",
	}

	var table = []struct {
		description                 string                      // description of test case
		catsrc                      v1alpha1.CatalogSource      // fake catalog source for testing
		serverVersion               *version.Info               // simulated kube version information
		gvks                        []*v1.PartialObjectMetadata // a fake kube manifest for testing gvk templates
		expectedGVKKeys             []GVK_Key                   // expected results after calling api
		expectedCatalogTemplate     string                      // expected results after calling api
		expectedUnresolvedTemplates []string                    // expected results after calling api
	}{
		{
			description:                 "no image templates",
			catsrc:                      v1alpha1.CatalogSource{},
			serverVersion:               &defaultServerVersion,
			gvks:                        []*v1.PartialObjectMetadata{},
			expectedGVKKeys:             []GVK_Key{},
			expectedCatalogTemplate:     "",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "kube image template",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s", TEMPL_KUBEMAJORV),
					},
				},
			},
			serverVersion:               &defaultServerVersion,
			gvks:                        []*v1.PartialObjectMetadata{},
			expectedGVKKeys:             []GVK_Key{},
			expectedCatalogTemplate:     "foo/v1",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "kube image template but no server version",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s", TEMPL_KUBEMAJORV),
					},
				},
			},
			serverVersion:               nil,
			gvks:                        []*v1.PartialObjectMetadata{},
			expectedGVKKeys:             []GVK_Key{},
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
			gvks:                        []*v1.PartialObjectMetadata{},
			expectedGVKKeys:             []GVK_Key{},
			expectedCatalogTemplate:     "foo/v{foobar}",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "kube image and gvk template",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s/%s", TEMPL_KUBEMAJORV, "{group:,version:v1,kind:Pod,name:foo,namespace:bar,jsonpath:{.metadata.annotations.dummy}}"),
					},
				},
			},
			serverVersion: &defaultServerVersion,
			gvks: []*v1.PartialObjectMetadata{
				{
					TypeMeta: v1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "foo",
						Namespace: "bar",
						Annotations: map[string]string{
							"dummy": "myimage",
						},
					},
				},
			},
			expectedGVKKeys: []GVK_Key{
				{
					GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					name:             "foo",
					namespace:        "bar",
				},
			},
			expectedCatalogTemplate:     "foo/v1/myimage",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "kube image with broken gvk template (jsonpath missing start and end curly braces)",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s/%s", TEMPL_KUBEMAJORV, "{group:,version:v1,kind:Pod,name:foo,namespace:bar,jsonpath:.metadata.annotations.dummy}"),
					},
				},
			},
			serverVersion: &defaultServerVersion,
			gvks: []*v1.PartialObjectMetadata{
				{
					TypeMeta: v1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "foo",
						Namespace: "bar",
						Annotations: map[string]string{
							"dummy": "myimage",
						},
					},
				},
			},
			expectedGVKKeys:             []GVK_Key{},
			expectedCatalogTemplate:     "foo/v1/{group:,version:v1,kind:Pod,name:foo,namespace:bar,jsonpath:.metadata.annotations.dummy}",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "kube image with broken gvk template (jsonpath unterminated array)",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s/%s", TEMPL_KUBEMAJORV, "{group:,version:v1,kind:Pod,name:foo,namespace:bar,jsonpath:{.metadata.annotations[.dummy}}"),
					},
				},
			},
			serverVersion: &defaultServerVersion,
			gvks: []*v1.PartialObjectMetadata{
				{
					TypeMeta: v1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "foo",
						Namespace: "bar",
						Annotations: map[string]string{
							"dummy": "myimage",
						},
					},
				},
			},
			expectedGVKKeys: []GVK_Key{
				{
					GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					name:             "foo",
					namespace:        "bar",
				},
			},
			expectedCatalogTemplate:     "foo/v1/missing",
			expectedUnresolvedTemplates: []string{"{group:,version:v1,kind:Pod,name:foo,namespace:bar,jsonpath:{.metadata.annotations[.dummy}}"},
		},
		{
			description: "kube image with broken gvk template (jsonpath with slice that does not exist)",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s/%s", TEMPL_KUBEMAJORV, "{group:,version:v1,kind:Pod,name:foo,namespace:bar,jsonpath:{.metadata.annotations[*].dummy}}"),
					},
				},
			},
			serverVersion: &defaultServerVersion,
			gvks: []*v1.PartialObjectMetadata{
				{
					TypeMeta: v1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "foo",
						Namespace: "bar",
						Annotations: map[string]string{
							"dummy": "myimage",
						},
					},
				},
			},
			expectedGVKKeys: []GVK_Key{
				{
					GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					name:             "foo",
					namespace:        "bar",
				},
			},
			expectedCatalogTemplate:     "foo/v1/missing",
			expectedUnresolvedTemplates: []string{"{group:,version:v1,kind:Pod,name:foo,namespace:bar,jsonpath:{.metadata.annotations[*].dummy}}"},
		},
		{
			description: "multiple gvk template",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s/%s", "{group:,version:v1,kind:Pod,name:fooA,namespace:bar,jsonpath:{.metadata.annotations.fooA}}", "{group:,version:v1,kind:Pod,name:fooB,namespace:bar,jsonpath:{.metadata.annotations.fooB}}"),
					},
				},
			},
			serverVersion: &defaultServerVersion,
			gvks: []*v1.PartialObjectMetadata{
				{
					TypeMeta: v1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "fooA",
						Namespace: "bar",
						Annotations: map[string]string{
							"fooA": "1",
						},
					},
				},
				{
					TypeMeta: v1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "fooB",
						Namespace: "bar",
						Annotations: map[string]string{
							"fooB": "myimage",
						},
					},
				},
			},
			expectedGVKKeys: []GVK_Key{
				{
					GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					name:             "fooA",
					namespace:        "bar",
				},
				{
					GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					name:             "fooB",
					namespace:        "bar",
				},
			},
			expectedCatalogTemplate:     "foo/v1/myimage",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "multiple gvk template - reusing same gvk",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s/%s", "{group:,version:v1,kind:Pod,name:fooA,namespace:bar,jsonpath:{.metadata.annotations.fooA}}", "{group:,version:v1,kind:Pod,name:fooA,namespace:bar,jsonpath:{.metadata.annotations.fooA}}"),
					},
				},
			},
			serverVersion: &defaultServerVersion,
			gvks: []*v1.PartialObjectMetadata{
				{
					TypeMeta: v1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "fooA",
						Namespace: "bar",
						Annotations: map[string]string{
							"fooA": "1",
						},
					},
				},
			},
			expectedGVKKeys: []GVK_Key{
				{
					GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					name:             "fooA",
					namespace:        "bar",
				},
			},
			expectedCatalogTemplate:     "foo/v1/1",
			expectedUnresolvedTemplates: []string{},
		},
		{
			description: "gvk template with missing object",
			catsrc: v1alpha1.CatalogSource{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						CatalogImageTemplateAnnotation: fmt.Sprintf("foo/v%s", "{group:,version:v1,kind:Pod,name:fooA,namespace:bar,jsonpath:{.metadata.annotations.fooA}}"),
					},
				},
			},
			serverVersion: &defaultServerVersion,
			gvks: []*v1.PartialObjectMetadata{
				nil,
			},
			expectedGVKKeys: []GVK_Key{
				{
					GroupVersionKind: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					name:             "fooA",
					namespace:        "bar",
				},
			},
			expectedCatalogTemplate:     "foo/vmissing",
			expectedUnresolvedTemplates: []string{"{group:,version:v1,kind:Pod,name:fooA,namespace:bar,jsonpath:{.metadata.annotations.fooA}}"},
		},
	}

	logger := logrus.New()

	for _, tt := range table {
		t.Run(tt.description, func(t *testing.T) {
			// make sure test case is initialized to default to simulate real usage when operator starts up
			resetMaps()

			gvkKeys := InitializeCatalogSourceTemplates(&tt.catsrc)
			assert.Equal(t, tt.expectedGVKKeys, gvkKeys, "did not get the expected keys after initializing image template")

			// simulate gvk watch updates
			for _, gvk := range tt.gvks {
				unst, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(gvk)
				u := unstructured.Unstructured{Object: unst}
				UpdateGVKValue(&u, logger)
			}

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
