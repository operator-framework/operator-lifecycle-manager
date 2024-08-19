package install

import (
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
)

// an intermediate type since authorizer.AttributesRecord is not orderable
type theAttributrix struct {
	User            user.Info
	Verb            string
	Namespace       string
	APIGroup        string
	APIVersion      string
	Resource        string
	Subresource     string
	Name            string
	ResourceRequest bool
	Path            string
}

type attributeSet map[theAttributrix]struct{}

// toAttributesSet converts the given user, namespace, and PolicyRule into a set of Attributes expected. This is useful for checking
// if a composed set of Roles/RoleBindings satisfies a PolicyRule.
func toAttributesSet(user user.Info, namespace string, rule rbacv1.PolicyRule) []authorizer.Attributes {
	set := attributeSet{}

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
						set[attributesRecord(user, namespace, verb, group, resource, name, nonResourceURL)] = struct{}{}
					}
				}
			}
		}
	}

	attributes := make([]authorizer.Attributes, len(set))
	i := 0
	for attribute := range set {
		attributes[i] = attribute.Attributes()
		i++
	}
	log.Debugf("attributes set %+v", attributes)

	return attributes
}

func (a theAttributrix) Attributes() authorizer.Attributes {
	return authorizer.AttributesRecord{
		User:            a.User,
		Verb:            a.Verb,
		Namespace:       a.Namespace,
		APIGroup:        a.APIGroup,
		Resource:        a.Resource,
		Name:            a.Name,
		ResourceRequest: a.ResourceRequest,
		Path:            a.Path,
	}
}

// attribute creates a new AttributesRecord with the given info. Currently RBAC authz only looks at user, verb, apiGroup, resource, and name.
func attributesRecord(user user.Info, namespace, verb, apiGroup, resource, name, path string) theAttributrix {
	resourceRequest := path == ""
	return theAttributrix{
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
