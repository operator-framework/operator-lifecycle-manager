package install

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
)

// toAttributesSet converts the given user, namespace, and PolicyRule into a set of Attributes expected. This is useful for checking
// if a composed set of Roles/RoleBindings satisfies a PolicyRule.
func toAttributesSet(user user.Info, namespace string, rule rbacv1.PolicyRule) []authorizer.Attributes {
	set := map[string]authorizer.Attributes{}

	// add empty string for empty groups, resources, resource names, and non resource urls
	groups := rule.APIGroups
	if len(groups) == 0 {
		groups = make([]string, 1)
	}
	resources := rule.Resources
	if len(resources) == 0 {
		resources = make([]string, 1)
	}
	names := rule.ResourceNames
	if len(names) == 0 {
		names = make([]string, 1)
	}
	nonResourceURLs := rule.NonResourceURLs
	if len(nonResourceURLs) == 0 {
		nonResourceURLs = make([]string, 1)
	}

	for _, verb := range rule.Verbs {
		for _, group := range groups {
			for _, resource := range resources {
				for _, name := range names {
					for _, nonResourceURL := range nonResourceURLs {
						attr := attributesRecord(user, namespace, verb, group, resource, name, nonResourceURL)
						key := attributesKey(user, namespace, verb, group, resource, name, nonResourceURL)
						set[key] = attr
					}
				}
			}
		}
	}

	attributes := make([]authorizer.Attributes, 0, len(set))
	for _, attribute := range set {
		attributes = append(attributes, attribute)
	}
	log.Debugf("attributes set %+v", attributes)

	return attributes
}

// attribute creates a new AttributesRecord with the given info. Currently RBAC authz only looks at user, verb, apiGroup, resource, and name.
func attributesRecord(user user.Info, namespace, verb, apiGroup, resource, name, path string) authorizer.AttributesRecord {
	resourceRequest := path == ""
	return authorizer.AttributesRecord{
		User:            user,
		Verb:            verb,
		Namespace:       namespace,
		APIGroup:        apiGroup,
		Resource:        resource,
		Name:            name,
		ResourceRequest: resourceRequest,
		Path:            path,
	}
}

func toDefaultInfo(sa *corev1.ServiceAccount) *user.DefaultInfo {
	// TODO(Nick): add Group if necessary
	return &user.DefaultInfo{
		Name: serviceaccount.MakeUsername(sa.GetNamespace(), sa.GetName()),
		UID:  string(sa.GetUID()),
	}
}

func attributesKey(user user.Info, namespace, verb, apiGroup, resource, name, path string) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s",
		user.GetName(),
		verb,
		namespace,
		apiGroup,
		resource,
		name,
		resource,
		path,
	)
}
