package projection_test

import (
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/stretchr/testify/assert"
)

func TestPropertiesAnnotationFromPropertyList(t *testing.T) {
	for _, tc := range []struct {
		name       string
		properties []*api.Property
		expected   string
		error      bool
	}{
		{
			name:       "nil property slice",
			properties: nil,
			expected:   "{}",
		},
		{
			name:       "empty property slice",
			properties: []*api.Property{},
			expected:   "{}",
		},
		{
			name: "invalid property value",
			properties: []*api.Property{{
				Type:  "bad",
				Value: `]`,
			}},
			error: true,
		},
		{
			name: "nonempty property slice",
			properties: []*api.Property{
				{
					Type:  "string",
					Value: `"hello"`,
				},
				{
					Type:  "number",
					Value: `5`,
				},
				{
					Type:  "array",
					Value: `[1,"two",3,"four"]`,
				}, {
					Type:  "object",
					Value: `{"hello":{"worl":"d"}}`,
				},
			},
			expected: `{"properties":[{"type":"string","value":"hello"},{"type":"number","value":5},{"type":"array","value":[1,"two",3,"four"]},{"type":"object","value":{"hello":{"worl":"d"}}}]}`,
		},
		{
			name: "unquoted string",
			properties: []*api.Property{
				{
					Type:  "version",
					Value: "4.8",
				},
			},
			expected: `{"properties":[{"type":"version","value":4.8}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := projection.PropertiesAnnotationFromPropertyList(tc.properties)
			assert := assert.New(t)
			assert.Equal(tc.expected, actual)
			if tc.error {
				assert.Error(err)
			} else {
				assert.NoError(err)
			}
		})
	}
}

func TestPropertyListFromPropertiesAnnotation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		annotation string
		expected   []*api.Property
		error      bool
	}{
		{
			name:       "empty",
			annotation: "",
			error:      true,
		},
		{
			name:       "invalid json",
			annotation: "]",
			error:      true,
		},
		{
			name:       "no properties key",
			annotation: "{}",
			expected:   nil,
		},
		{
			name:       "properties value not an array or null",
			annotation: `{"properties":5}`,
			error:      true,
		},
		{
			name:       "property element not an object",
			annotation: `{"properties":[42]}`,
			error:      true,
		},
		{
			name:       "no properties",
			annotation: `{"properties":[]}`,
			expected:   nil,
		},
		{
			name:       "several properties",
			annotation: `{"properties":[{"type":"string","value":"hello"},{"type":"number","value":5},{"type":"array","value":[1,"two",3,"four"]},{"type":"object","value":{"hello":{"worl":"d"}}}]}`,
			expected: []*api.Property{
				{
					Type:  "string",
					Value: `"hello"`,
				},
				{
					Type:  "number",
					Value: `5`,
				},
				{
					Type:  "array",
					Value: `[1,"two",3,"four"]`,
				},
				{
					Type:  "object",
					Value: `{"hello":{"worl":"d"}}`,
				},
			},
		},
		{
			name:       "unquoted string values",
			annotation: `{"properties":[{"type": "version","value": 4.8}]}`,
			expected: []*api.Property{
				{
					Type:  "version",
					Value: "4.8",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := projection.PropertyListFromPropertiesAnnotation(tc.annotation)
			assert := assert.New(t)
			assert.Equal(tc.expected, actual)
			if tc.error {
				assert.Error(err)
			} else {
				assert.NoError(err)
			}
		})
	}
}
