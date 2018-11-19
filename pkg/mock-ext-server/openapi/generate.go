package openapi

import (
	"github.com/go-openapi/spec"
	common "k8s.io/kube-openapi/pkg/common"
)

func GetOpenAPIDefinitions(ref common.ReferenceCallback) map[string]common.OpenAPIDefinition {
	return map[string]common.OpenAPIDefinition{
		"github.com/operator-framework/operator-lifecycle-manager/pkg/mock-ext-server/apis/anything/v1alpha1.Anything": MockOpenAPIDefinition(),
		"k8s.io/apimachinery/pkg/version.Info":                           schema_k8sio_apimachinery_pkg_version_Info(ref),
		"k8s.io/apimachinery/pkg/apis/meta/v1.ServerAddressByClientCIDR": schema_pkg_apis_meta_v1_ServerAddressByClientCIDR(ref),
		"k8s.io/apimachinery/pkg/apis/meta/v1.GroupVersionForDiscovery":  schema_pkg_apis_meta_v1_GroupVersionForDiscovery(ref),
		"k8s.io/apimachinery/pkg/apis/meta/v1.APIGroup":                  schema_pkg_apis_meta_v1_APIGroup(ref),
		"k8s.io/apimachinery/pkg/apis/meta/v1.APIGroupList":              schema_pkg_apis_meta_v1_APIGroupList(ref),
		"k8s.io/apimachinery/pkg/apis/meta/v1.APIResource":               schema_pkg_apis_meta_v1_APIResource(ref),
		"k8s.io/apimachinery/pkg/apis/meta/v1.APIResourceList":           schema_pkg_apis_meta_v1_APIResourceList(ref),
	}
}

// MockOpenAPIDefinition returns a top-level OpenAPIDefinition with a single, meaningless field
func MockOpenAPIDefinition() common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "A mock resource with a single property.",
				Properties: map[string]spec.Schema{
					"prop": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
				},
			},
		},
		Dependencies: []string{},
	}
}

func schema_pkg_apis_meta_v1_APIResource(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "APIResource specifies the name of a resource and whether it is namespaced.",
				Properties: map[string]spec.Schema{
					"name": {
						SchemaProps: spec.SchemaProps{
							Description: "name is the plural name of the resource.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"singularName": {
						SchemaProps: spec.SchemaProps{
							Description: "singularName is the singular name of the resource.  This allows clients to handle plural and singular opaquely. The singularName is more correct for reporting status on a single item and both singular and plural are allowed from the kubectl CLI interface.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"namespaced": {
						SchemaProps: spec.SchemaProps{
							Description: "namespaced indicates if a resource is namespaced or not.",
							Type:        []string{"boolean"},
							Format:      "",
						},
					},
					"group": {
						SchemaProps: spec.SchemaProps{
							Description: "group is the preferred group of the resource.  Empty implies the group of the containing resource list. For subresources, this may have a different value, for example: Scale\".",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"version": {
						SchemaProps: spec.SchemaProps{
							Description: "version is the preferred version of the resource.  Empty implies the version of the containing resource list For subresources, this may have a different value, for example: v1 (while inside a v1beta1 version of the core resource's group)\".",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "kind is the kind for the resource (e.g. 'Foo' is the kind for a resource 'foo')",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"verbs": {
						SchemaProps: spec.SchemaProps{
							Description: "verbs is a list of supported kube verbs (this includes get, list, watch, create, update, patch, delete, deletecollection, and proxy)",
							Type:        []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Type:   []string{"string"},
										Format: "",
									},
								},
							},
						},
					},
					"shortNames": {
						SchemaProps: spec.SchemaProps{
							Description: "shortNames is a list of suggested short names of the resource.",
							Type:        []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Type:   []string{"string"},
										Format: "",
									},
								},
							},
						},
					},
					"categories": {
						SchemaProps: spec.SchemaProps{
							Description: "categories is a list of the grouped resources this resource belongs to (e.g. 'all')",
							Type:        []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Type:   []string{"string"},
										Format: "",
									},
								},
							},
						},
					},
				},
				Required: []string{"name", "singularName", "namespaced", "kind", "verbs"},
			},
		},
		Dependencies: []string{},
	}
}

func schema_pkg_apis_meta_v1_APIResourceList(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "APIResourceList is a list of APIResource, it is used to expose the name of the resources supported in a specific group and version, and if the resource is namespaced.",
				Properties: map[string]spec.Schema{
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"apiVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#resources",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"groupVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "groupVersion is the group and version this APIResourceList is for.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"resources": {
						SchemaProps: spec.SchemaProps{
							Description: "resources contains the name of the resources and if they are namespaced.",
							Type:        []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Ref: ref("k8s.io/apimachinery/pkg/apis/meta/v1.APIResource"),
									},
								},
							},
						},
					},
				},
				Required: []string{"groupVersion", "resources"},
			},
		},
		Dependencies: []string{
			"k8s.io/apimachinery/pkg/apis/meta/v1.APIResource"},
	}
}

func schema_pkg_apis_meta_v1_ServerAddressByClientCIDR(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "ServerAddressByClientCIDR helps the client to determine the server address that they should use, depending on the clientCIDR that they match.",
				Properties: map[string]spec.Schema{
					"clientCIDR": {
						SchemaProps: spec.SchemaProps{
							Description: "The CIDR with which clients can match their IP to figure out the server address that they should use.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"serverAddress": {
						SchemaProps: spec.SchemaProps{
							Description: "Address of this server, suitable for a client that matches the above CIDR. This can be a hostname, hostname:port, IP or IP:port.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
				},
				Required: []string{"clientCIDR", "serverAddress"},
			},
		},
		Dependencies: []string{},
	}
}

func schema_pkg_apis_meta_v1_GroupVersionForDiscovery(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "GroupVersion contains the \"group/version\" and \"version\" string of a version. It is made a struct to keep extensibility.",
				Properties: map[string]spec.Schema{
					"groupVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "groupVersion specifies the API group and version in the form \"group/version\"",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"version": {
						SchemaProps: spec.SchemaProps{
							Description: "version specifies the version in the form of \"version\". This is to save the clients the trouble of splitting the GroupVersion.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
				},
				Required: []string{"groupVersion", "version"},
			},
		},
		Dependencies: []string{},
	}
}

func schema_pkg_apis_meta_v1_APIGroup(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "APIGroup contains the name, the supported versions, and the preferred version of a group.",
				Properties: map[string]spec.Schema{
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"apiVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#resources",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"name": {
						SchemaProps: spec.SchemaProps{
							Description: "name is the name of the group.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"versions": {
						SchemaProps: spec.SchemaProps{
							Description: "versions are the versions supported in this group.",
							Type:        []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Ref: ref("k8s.io/apimachinery/pkg/apis/meta/v1.GroupVersionForDiscovery"),
									},
								},
							},
						},
					},
					"preferredVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "preferredVersion is the version preferred by the API server, which probably is the storage version.",
							Ref:         ref("k8s.io/apimachinery/pkg/apis/meta/v1.GroupVersionForDiscovery"),
						},
					},
					"serverAddressByClientCIDRs": {
						SchemaProps: spec.SchemaProps{
							Description: "a map of client CIDR to server address that is serving this group. This is to help clients reach servers in the most network-efficient way possible. Clients can use the appropriate server address as per the CIDR that they match. In case of multiple matches, clients should use the longest matching CIDR. The server returns only those CIDRs that it thinks that the client can match. For example: the master will return an internal IP CIDR only, if the client reaches the server using an internal IP. Server looks at X-Forwarded-For header or X-Real-Ip header or request.RemoteAddr (in that order) to get the client IP.",
							Type:        []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Ref: ref("k8s.io/apimachinery/pkg/apis/meta/v1.ServerAddressByClientCIDR"),
									},
								},
							},
						},
					},
				},
				Required: []string{"name", "versions"},
			},
		},
		Dependencies: []string{
			"k8s.io/apimachinery/pkg/apis/meta/v1.GroupVersionForDiscovery", "k8s.io/apimachinery/pkg/apis/meta/v1.ServerAddressByClientCIDR"},
	}
}

func schema_pkg_apis_meta_v1_APIGroupList(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "APIGroupList is a list of APIGroup, to allow clients to discover the API at /apis.",
				Properties: map[string]spec.Schema{
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"apiVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#resources",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"groups": {
						SchemaProps: spec.SchemaProps{
							Description: "groups is a list of APIGroup.",
							Type:        []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Ref: ref("k8s.io/apimachinery/pkg/apis/meta/v1.APIGroup"),
									},
								},
							},
						},
					},
				},
				Required: []string{"groups"},
			},
		},
		Dependencies: []string{
			"k8s.io/apimachinery/pkg/apis/meta/v1.APIGroup"},
	}
}

func schema_k8sio_apimachinery_pkg_version_Info(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "Info contains versioning information. how we'll want to distribute that information.",
				Properties: map[string]spec.Schema{
					"major": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"minor": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"gitVersion": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"gitCommit": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"gitTreeState": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"buildDate": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"goVersion": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"compiler": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
					"platform": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "",
						},
					},
				},
				Required: []string{"major", "minor", "gitVersion", "gitCommit", "gitTreeState", "buildDate", "goVersion", "compiler", "platform"},
			},
		},
		Dependencies: []string{},
	}
}
