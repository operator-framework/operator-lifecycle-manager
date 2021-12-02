package runtime_constraints

import (
	"encoding/json"
	"io/ioutil"
	"os"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/pkg/errors"
)

const (
	maxRuntimeConstraints       = 10
	RuntimeConstraintEnvVarName = "RUNTIME_CONSTRAINTS_FILE_PATH"
)

type RuntimeConstraintsProvider struct {
	runtimeConstraints []cache.Predicate
}

func (p *RuntimeConstraintsProvider) Constraints() []cache.Predicate {
	return p.runtimeConstraints
}

func New(runtimeConstraints []cache.Predicate) *RuntimeConstraintsProvider {
	return &RuntimeConstraintsProvider{
		runtimeConstraints: runtimeConstraints,
	}
}

func NewFromEnv() (*RuntimeConstraintsProvider, error) {
	runtimeConstraintsFilePath, isSet := os.LookupEnv(RuntimeConstraintEnvVarName)
	if !isSet {
		return nil, nil
	}
	return NewFromFile(runtimeConstraintsFilePath)
}

func NewFromFile(runtimeConstraintsFilePath string) (*RuntimeConstraintsProvider, error) {
	propertiesFile, err := readRuntimeConstraintsYaml(runtimeConstraintsFilePath)
	if err != nil {
		return nil, err
	}

	// Using package type to test with
	// We may only want to allow the generic constraint types once they are readym
	var constraints = make([]cache.Predicate, 0)
	for _, property := range propertiesFile.Properties {
		rawMessage := []byte(property.Value)
		switch property.Type {
		case registry.PackageType:
			dep := registry.PackageDependency{}
			err := json.Unmarshal(rawMessage, &dep)
			if err != nil {
				return nil, err
			}
			constraints = append(constraints, cache.PkgPredicate(dep.PackageName))
		case registry.LabelType:
			dep := registry.LabelDependency{}
			err := json.Unmarshal(rawMessage, &dep)
			if err != nil {
				return nil, err
			}
			constraints = append(constraints, cache.LabelPredicate(dep.Label))
		}
		if len(constraints) >= maxRuntimeConstraints {
			return nil, errors.Errorf("Too many runtime constraints defined (%d/%d)", len(constraints), maxRuntimeConstraints)
		}
	}

	return New(constraints), nil
}

func readRuntimeConstraintsYaml(yamlPath string) (*registry.PropertiesFile, error) {
	// Read file
	yamlFile, err := ioutil.ReadFile(yamlPath)
	if err != nil {
		return nil, err
	}

	// Parse yaml
	var propertiesFile = &registry.PropertiesFile{}
	err = json.Unmarshal(yamlFile, propertiesFile)
	if err != nil {
		return nil, err
	}

	return propertiesFile, nil
}
