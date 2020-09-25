package projection

import (
	"encoding/json"
	"fmt"

	"github.com/operator-framework/operator-registry/pkg/api"
)

const (
	PropertiesAnnotationKey = "operatorframework.io/properties"
)

type property struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

type propertiesAnnotation struct {
	Properties []property `json:"properties,omitempty"`
}

func PropertiesAnnotationFromPropertyList(props []*api.Property) (string, error) {
	var anno propertiesAnnotation
	for _, prop := range props {
		anno.Properties = append(anno.Properties, property{
			Type:  prop.Type,
			Value: json.RawMessage(prop.Value),
		})
	}
	v, err := json.Marshal(&anno)
	if err != nil {
		return "", fmt.Errorf("failed to marshal properties annotation: %w", err)
	}
	return string(v), nil
}

func PropertyListFromPropertiesAnnotation(raw string) ([]*api.Property, error) {
	var anno propertiesAnnotation
	if err := json.Unmarshal([]byte(raw), &anno); err != nil {
		return nil, fmt.Errorf("failed to unmarshal properties annotation: %w", err)
	}
	var result []*api.Property
	for _, each := range anno.Properties {
		result = append(result, &api.Property{
			Type:  each.Type,
			Value: string(each.Value),
		})
	}
	return result, nil
}
