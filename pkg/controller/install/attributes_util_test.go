package install

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
)

func TestToAttributeSet(t *testing.T) {
	user := &user.DefaultInfo{
		Name: "Jim",
	}
	namespace := "olm"

	tests := []struct {
		description        string
		rule               rbacv1.PolicyRule
		expectedAttributes map[string]authorizer.AttributesRecord
	}{
		{
			description: "SimpleRule",
			rule: rbacv1.PolicyRule{
				Verbs:     []string{"*"},
				APIGroups: []string{"*"},
				Resources: []string{"*"},
			},
			expectedAttributes: map[string]authorizer.AttributesRecord{
				attributesKey(user, namespace, "*", "*", "*", "", ""): attributesRecord(user, namespace, "*", "*", "*", "", ""),
			},
		},
		{
			description: "SimpleNonResourceRule",
			rule: rbacv1.PolicyRule{
				Verbs:           []string{"*"},
				NonResourceURLs: []string{"/api"},
			},
			expectedAttributes: map[string]authorizer.AttributesRecord{
				attributesKey(user, namespace, "*", "", "", "", "/api"): attributesRecord(user, namespace, "*", "", "", "", "/api"),
			},
		},
		{
			description: "SeparateVerbs",
			rule: rbacv1.PolicyRule{
				Verbs:     []string{"create", "delete"},
				APIGroups: []string{"*"},
				Resources: []string{"*"},
			},
			expectedAttributes: map[string]authorizer.AttributesRecord{
				attributesKey(user, namespace, "create", "*", "*", "", ""): attributesRecord(user, namespace, "create", "*", "*", "", ""),
				attributesKey(user, namespace, "delete", "*", "*", "", ""): attributesRecord(user, namespace, "delete", "*", "*", "", ""),
			},
		},
		{
			description: "MultipleResources",
			rule: rbacv1.PolicyRule{
				Verbs:     []string{"get", "update"},
				Resources: []string{"donuts", "coffee"},
			},
			expectedAttributes: map[string]authorizer.AttributesRecord{
				attributesKey(user, namespace, "get", "", "donuts", "", ""):    attributesRecord(user, namespace, "get", "", "donuts", "", ""),
				attributesKey(user, namespace, "update", "", "donuts", "", ""): attributesRecord(user, namespace, "update", "", "donuts", "", ""),
				attributesKey(user, namespace, "get", "", "coffee", "", ""):    attributesRecord(user, namespace, "get", "", "coffee", "", ""),
				attributesKey(user, namespace, "update", "", "coffee", "", ""): attributesRecord(user, namespace, "update", "", "coffee", "", ""),
			},
		},
		{
			description: "MultipleNonResourceURLs",
			rule: rbacv1.PolicyRule{
				Verbs:           []string{"*"},
				NonResourceURLs: []string{"/capybaras", "/caviidaes"},
			},
			expectedAttributes: map[string]authorizer.AttributesRecord{
				attributesKey(user, namespace, "*", "", "", "", "/capybaras"): attributesRecord(user, namespace, "*", "", "", "", "/capybaras"),
				attributesKey(user, namespace, "*", "", "", "", "/caviidaes"): attributesRecord(user, namespace, "*", "", "", "", "/caviidaes"),
			},
		},
		{
			description: "MultipleResourcesWithResourceName",
			rule: rbacv1.PolicyRule{
				Verbs:         []string{"get", "update"},
				Resources:     []string{"donuts", "coffee"},
				ResourceNames: []string{"nyc"},
			},
			expectedAttributes: map[string]authorizer.AttributesRecord{
				attributesKey(user, namespace, "get", "", "donuts", "nyc", ""):    attributesRecord(user, namespace, "get", "", "donuts", "nyc", ""),
				attributesKey(user, namespace, "update", "", "donuts", "nyc", ""): attributesRecord(user, namespace, "update", "", "donuts", "nyc", ""),
				attributesKey(user, namespace, "get", "", "coffee", "nyc", ""):    attributesRecord(user, namespace, "get", "", "coffee", "nyc", ""),
				attributesKey(user, namespace, "update", "", "coffee", "nyc", ""): attributesRecord(user, namespace, "update", "", "coffee", "nyc", ""),
			},
		},
		{
			description: "MultipleResourcesWithMultipleAPIGroups",
			rule: rbacv1.PolicyRule{
				Verbs:         []string{"get", "update"},
				Resources:     []string{"donuts", "coffee"},
				APIGroups:     []string{"apps.coreos.com", "apps.redhat.com"},
				ResourceNames: []string{"nyc"},
			},
			expectedAttributes: map[string]authorizer.AttributesRecord{
				attributesKey(user, namespace, "get", "apps.coreos.com", "donuts", "nyc", ""):    attributesRecord(user, namespace, "get", "apps.coreos.com", "donuts", "nyc", ""),
				attributesKey(user, namespace, "update", "apps.coreos.com", "donuts", "nyc", ""): attributesRecord(user, namespace, "update", "apps.coreos.com", "donuts", "nyc", ""),
				attributesKey(user, namespace, "get", "apps.coreos.com", "coffee", "nyc", ""):    attributesRecord(user, namespace, "get", "apps.coreos.com", "coffee", "nyc", ""),
				attributesKey(user, namespace, "update", "apps.coreos.com", "coffee", "nyc", ""): attributesRecord(user, namespace, "update", "apps.coreos.com", "coffee", "nyc", ""),
				attributesKey(user, namespace, "get", "apps.redhat.com", "donuts", "nyc", ""):    attributesRecord(user, namespace, "get", "apps.redhat.com", "donuts", "nyc", ""),
				attributesKey(user, namespace, "update", "apps.redhat.com", "donuts", "nyc", ""): attributesRecord(user, namespace, "update", "apps.redhat.com", "donuts", "nyc", ""),
				attributesKey(user, namespace, "get", "apps.redhat.com", "coffee", "nyc", ""):    attributesRecord(user, namespace, "get", "apps.redhat.com", "coffee", "nyc", ""),
				attributesKey(user, namespace, "update", "apps.redhat.com", "coffee", "nyc", ""): attributesRecord(user, namespace, "update", "apps.redhat.com", "coffee", "nyc", ""),
			},
		},
		{
			description:        "NoVerbs",
			rule:               rbacv1.PolicyRule{},
			expectedAttributes: map[string]authorizer.AttributesRecord{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			attributesSet := toAttributesSet(user, namespace, tt.rule)

			require.Equal(t, len(tt.expectedAttributes), len(attributesSet))

			for _, attributes := range attributesSet {
				// type assert as AttributesRecord
				a, ok := attributes.(authorizer.AttributesRecord)
				require.True(t, ok, "type assertion for attributes failed")

				// make sure we're expecting the attribute
				key := attributesKey(a.GetUser(), a.GetNamespace(), a.GetVerb(), a.GetAPIGroup(), a.GetResource(), a.GetName(), a.GetPath())
				_, exists := tt.expectedAttributes[key]
				require.True(t, exists, fmt.Sprintf("found unexpected attributes %v", attributes))

				// ensure each expected attribute only appears once
				delete(tt.expectedAttributes, key)
			}

			// check that all expected have been found
			require.Zero(t, len(tt.expectedAttributes), fmt.Sprintf("%d expected attributes not found", len(tt.expectedAttributes)))
		})
	}
}
