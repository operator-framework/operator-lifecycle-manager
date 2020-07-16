package solver

// Identifier values uniquely identify particular Installables within
// the input to a single call to Solve.
type Identifier string

func (id Identifier) String() string {
	return string(id)
}

// Installable values are the basic unit of problems and solutions
// understood by this package.
type Installable interface {
	// Identifier returns the Identifier that uniquely identifies
	// this Installable among all other Installables in a given
	// problem.
	Identifier() Identifier
	// Constraints returns the set of constraints that apply to
	// this Installable.
	Constraints() []Constraint
}

// zeroInstallable is returned by InstallableOf in error cases.
type zeroInstallable struct{}

var _ Installable = zeroInstallable{}

func (zeroInstallable) Identifier() Identifier {
	return ""
}

func (zeroInstallable) Constraints() []Constraint {
	return nil
}
