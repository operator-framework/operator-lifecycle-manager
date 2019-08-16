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
		expectedAttributes map[authorizer.AttributesRecord]struct{}
	}{
		{
			description: "SimpleRule",
			rule: rbacv1.PolicyRule{
				Verbs:     []string{"*"},
				APIGroups: []string{"*"},
				Resources: []string{"*"},
			},
			expectedAttributes: map[authorizer.AttributesRecord]struct{}{
				attributesRecord(user, namespace, "*", "*", "*", "", ""): {},
			},
		},
		{
			description: "SimpleNonResourceRule",
			rule: rbacv1.PolicyRule{
				Verbs:           []string{"*"},
				NonResourceURLs: []string{"/api"},
			},
			expectedAttributes: map[authorizer.AttributesRecord]struct{}{
				attributesRecord(user, namespace, "*", "", "", "", "/api"): {},
			},
		},
		{
			description: "SeparateVerbs",
			rule: rbacv1.PolicyRule{
				Verbs:     []string{"create", "delete"},
				APIGroups: []string{"*"},
				Resources: []string{"*"},
			},
			expectedAttributes: map[authorizer.AttributesRecord]struct{}{
				attributesRecord(user, namespace, "create", "*", "*", "", ""): {},
				attributesRecord(user, namespace, "delete", "*", "*", "", ""): {},
			},
		},
		{
			description: "MultipleResources",
			rule: rbacv1.PolicyRule{
				Verbs:     []string{"get", "update"},
				Resources: []string{"donuts", "coffee"},
			},
			expectedAttributes: map[authorizer.AttributesRecord]struct{}{
				attributesRecord(user, namespace, "get", "", "donuts", "", ""):    {},
				attributesRecord(user, namespace, "update", "", "donuts", "", ""): {},
				attributesRecord(user, namespace, "get", "", "coffee", "", ""):    {},
				attributesRecord(user, namespace, "update", "", "coffee", "", ""): {},
			},
		},
		{
			description: "MultipleNonResourceURLs",
			rule: rbacv1.PolicyRule{
				Verbs:           []string{"*"},
				NonResourceURLs: []string{"/capybaras", "/caviidaes"},
			},
			expectedAttributes: map[authorizer.AttributesRecord]struct{}{
				attributesRecord(user, namespace, "*", "", "", "", "/capybaras"): {},
				attributesRecord(user, namespace, "*", "", "", "", "/caviidaes"): {},
			},
		},
		{
			description: "MultipleResourcesWithResourceName",
			rule: rbacv1.PolicyRule{
				Verbs:         []string{"get", "update"},
				Resources:     []string{"donuts", "coffee"},
				ResourceNames: []string{"nyc"},
			},
			expectedAttributes: map[authorizer.AttributesRecord]struct{}{
				attributesRecord(user, namespace, "get", "", "donuts", "nyc", ""):    {},
				attributesRecord(user, namespace, "update", "", "donuts", "nyc", ""): {},
				attributesRecord(user, namespace, "get", "", "coffee", "nyc", ""):    {},
				attributesRecord(user, namespace, "update", "", "coffee", "nyc", ""): {},
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
			expectedAttributes: map[authorizer.AttributesRecord]struct{}{
				attributesRecord(user, namespace, "get", "apps.coreos.com", "donuts", "nyc", ""):    {},
				attributesRecord(user, namespace, "update", "apps.coreos.com", "donuts", "nyc", ""): {},
				attributesRecord(user, namespace, "get", "apps.coreos.com", "coffee", "nyc", ""):    {},
				attributesRecord(user, namespace, "update", "apps.coreos.com", "coffee", "nyc", ""): {},
				attributesRecord(user, namespace, "get", "apps.redhat.com", "donuts", "nyc", ""):    {},
				attributesRecord(user, namespace, "update", "apps.redhat.com", "donuts", "nyc", ""): {},
				attributesRecord(user, namespace, "get", "apps.redhat.com", "coffee", "nyc", ""):    {},
				attributesRecord(user, namespace, "update", "apps.redhat.com", "coffee", "nyc", ""): {},
			},
		},
		{
			description:        "NoVerbs",
			rule:               rbacv1.PolicyRule{},
			expectedAttributes: map[authorizer.AttributesRecord]struct{}{},
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
				_, exists := tt.expectedAttributes[a]
				require.True(t, exists, fmt.Sprintf("found unexpected attributes %v", attributes))

				// ensure each expected attribute only appears once
				delete(tt.expectedAttributes, a)
			}

			// check that all expected have been found
			require.Zero(t, len(tt.expectedAttributes), fmt.Sprintf("%d expected attributes not found", len(tt.expectedAttributes)))
		})
	}
}
