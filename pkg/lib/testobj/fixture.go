package testobj

import (
	"fmt"
	"os"
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// RuntimeMetaObject is an object with both runtime and metadata level info.
type RuntimeMetaObject interface {
	runtime.Object
	metav1.Object
}

// FixtureFiller knows how to fill fixtures.
type FixtureFiller interface {
	// NewFixture populates a given fixture.
	Fill(fixture RuntimeMetaObject) RuntimeMetaObject
}

// FixtureFillerFunc is a function that implements FixtureFiller.
type FixtureFillerFunc func(fixture RuntimeMetaObject) RuntimeMetaObject

// Fill invokes a FixtureFillerFunc on a fixture.
func (f FixtureFillerFunc) Fill(fixture RuntimeMetaObject) RuntimeMetaObject {
	return f(fixture)
}

// TypedFixtureFiller is a set of fillers keyed by fixture type.
type TypedFixtureFiller struct {
	fillers map[reflect.Type]FixtureFiller
}

// Fill populates a fixture if an associated filler has been defined for its type.
// Panics if there's no filler defined for the fixture type.
func (t *TypedFixtureFiller) Fill(fixture RuntimeMetaObject) RuntimeMetaObject {
	if t.fillers == nil {
		t.fillers = map[reflect.Type]FixtureFiller{}
	}

	// Pick out the correct filler and pass the buck
	ft := reflect.TypeOf(fixture)
	filler := t.fillers[ft]
	if filler == nil {
		panic(fmt.Errorf("unrecognized fixture type: %t", fixture))
	}

	return filler.Fill(fixture)
}

// PrototypeFiller is a Filler that copies existing fields from a prototypical instance of a fixture.
type PrototypeFiller struct {
	prototype RuntimeMetaObject
}

// Fill populates a fixture by copying a prototypical fixture.
// Panics if a given fixture is not the same type as the prototype.
func (p *PrototypeFiller) Fill(fixture RuntimeMetaObject) RuntimeMetaObject {
	// Copy p.proto, fill with fixture meta, and set underlying value
	if reflect.TypeOf(fixture) != reflect.TypeOf(p.prototype) {
		panic(fmt.Errorf("wrong fixture type for filler, have %t want %t", fixture, p.prototype))
	}

	c := p.prototype.DeepCopyObject()
	vp := reflect.ValueOf(c).Elem()
	reflect.ValueOf(fixture).Elem().Set(vp) // TODO(njhale): do we care about recovering from panics in fixture fillers?

	return fixture
}

type fixtureFile struct {
	file    string
	fixture RuntimeMetaObject
}

// FixtureFillerConfig holds the configuration needed to build a FixtureFiller.
type FixtureFillerConfig struct {
	fixtureFiles      []fixtureFile
	fixturePrototypes []RuntimeMetaObject
}

func (c *FixtureFillerConfig) apply(options []FixtureFillerOption) {
	for _, option := range options {
		option(c)
	}
}

// FixtureFillerOption represents a configuration option for building a FixtureFiller.
type FixtureFillerOption func(*FixtureFillerConfig)

// WithFixtureFile configures a FixtureFiller to use a file to populate fixtures of a given type.
func WithFixtureFile(fixture RuntimeMetaObject, file string) FixtureFillerOption {
	return func(config *FixtureFillerConfig) {
		config.fixtureFiles = append(config.fixtureFiles, fixtureFile{fixture: fixture, file: file})
	}
}

// WithFixture configures a FixtureFiller to copy the contents of the given fixture to fixtures of the same type.
func WithFixture(fixture RuntimeMetaObject) FixtureFillerOption {
	return func(config *FixtureFillerConfig) {
		config.fixturePrototypes = append(config.fixturePrototypes, fixture)
	}
}

// NewFixtureFiller builds and returns a new FixtureFiller.
func NewFixtureFiller(options ...FixtureFillerOption) *TypedFixtureFiller {
	config := &FixtureFillerConfig{}
	config.apply(options)

	// Load files and generate filters by type
	typed := &TypedFixtureFiller{
		fillers: map[reflect.Type]FixtureFiller{},
	}
	for _, fixtureFile := range config.fixtureFiles {
		file := fixtureFile.file
		fixture := fixtureFile.fixture.DeepCopyObject()
		err := func() error {
			// TODO(njhale): DI file decoder
			fileReader, err := os.Open(file)
			if err != nil {
				return fmt.Errorf("unable to read file %s: %s", file, err)
			}
			defer fileReader.Close()

			decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)

			return decoder.Decode(fixture)
		}()

		if err != nil {
			panic(err)
		}

		typed.fillers[reflect.TypeOf(fixture)] = &PrototypeFiller{prototype: fixture.(RuntimeMetaObject)}
	}

	// Load in-memory fixtures
	for _, prototype := range config.fixturePrototypes {
		if prototype == nil {
			panic("nil fixtures not allowed")
		}

		typed.fillers[reflect.TypeOf(prototype)] = &PrototypeFiller{prototype: prototype}
	}

	return typed
}
