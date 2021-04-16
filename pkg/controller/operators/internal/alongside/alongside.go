// Package alongside provides a mechanism for recording the fact that
// one object was installed alongside another object as part of the
// installation of the same operator version.
package alongside

import (
	"fmt"
	"hash/fnv"
	"strings"
)

const (
	prefix = "operatorframework.io/installed-alongside-"
)

// NamespacedName is a reference to an object by namespace and name.
type NamespacedName struct {
	Namespace string
	Name      string
}

type Annotatable interface {
	GetAnnotations() map[string]string
	SetAnnotations(map[string]string)
}

// Annotator translates installed-alongside references to and from
// object annotations.
type Annotator struct{}

// FromObject returns a slice containing each namespaced name
// referenced by an alongside annotation on the provided Object.
func (a Annotator) FromObject(o Annotatable) []NamespacedName {
	var result []NamespacedName
	for k, v := range o.GetAnnotations() {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		tokens := strings.Split(v, "/")
		if len(tokens) != 2 {
			continue
		}
		result = append(result, NamespacedName{
			Namespace: tokens[0],
			Name:      tokens[1],
		})
	}
	return result
}

// ToObject removes all existing alongside annotations on the provided
// Object and adds one new annotation per entry in the provided slice
// of namespaced names.
func (a Annotator) ToObject(o Annotatable, nns []NamespacedName) {
	annotations := o.GetAnnotations()

	for key := range annotations {
		if strings.HasPrefix(key, prefix) {
			delete(annotations, key)
		}
	}

	if len(nns) == 0 {
		if len(annotations) == 0 {
			annotations = nil
		}
		o.SetAnnotations(annotations)
		return
	}

	if annotations == nil {
		annotations = make(map[string]string, len(nns))
	}
	for _, nn := range nns {
		annotations[key(nn)] = fmt.Sprintf("%s/%s", nn.Namespace, nn.Name)
	}
	o.SetAnnotations(annotations)
}

func key(n NamespacedName) string {
	hasher := fnv.New64a()
	hasher.Write([]byte(n.Namespace))
	hasher.Write([]byte{'/'})
	hasher.Write([]byte(n.Name))
	return fmt.Sprintf("%s%x", prefix, hasher.Sum64())
}
