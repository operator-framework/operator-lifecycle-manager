package image

import "fmt"

// Reference describes a reference to a container image.
type Reference interface {
	fmt.Stringer
}

// SimpleReference is a reference backed by a string with no additional validation.
type SimpleReference string

func (s SimpleReference) String() string {
	ref := string(s)
	return ref
}
