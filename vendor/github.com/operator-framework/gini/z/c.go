package z

import "fmt"

// C is a clause ref.  Clause refs are ephemeral and
// may change value during solves. Clause refs are used by objects
// implementing inter.CnfSimp
type C uint32

func (p C) String() string {
	return fmt.Sprintf("c%d", p)
}
