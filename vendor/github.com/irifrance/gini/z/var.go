// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package z

import "fmt"

// Type Var is a representation of a Boolean variable.
type Var uint32

func (v Var) String() string {
	return fmt.Sprintf("v%d", v)
}

// Pos returns a Lit which is v.
func (v Var) Pos() Lit {
	return Lit(v << 1)
}

// Neg regurns a Lit which is not(v)
func (v Var) Neg() Lit {
	return Lit((v << 1) | 1)
}
