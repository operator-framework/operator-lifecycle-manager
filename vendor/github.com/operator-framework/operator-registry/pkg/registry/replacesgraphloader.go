package registry

import (
	"fmt"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type ReplacesGraphLoader struct {
}

// CanAdd checks that a new bundle can be added in replaces mode (i.e. the replaces
// defined for the bundle already exists)
func (r *ReplacesGraphLoader) CanAdd(bundle *Bundle, graph *Package) (bool, error) {
	replaces, err := bundle.Replaces()
	if err != nil {
		return false, fmt.Errorf("Invalid content, unable to parse bundle")
	}

	csvName := bundle.Name

	// adding the first bundle in the graph
	if replaces == "" {
		return true, nil
	}

	var errs []error

	// check that the bundle can be added
	if !graph.HasCsv(replaces) {
		err := fmt.Errorf("Invalid bundle %s, bundle specifies a non-existent replacement %s", csvName, replaces)
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return false, utilerrors.NewAggregate(errs)
	}

	return true, nil
}
