package generator

import (
	"errors"
	"go/types"
)

func (f *Fake) loadMethodForFunction() error {
	t, ok := f.Target.Type().(*types.Named)
	if !ok {
		return errors.New("target is not a named type")
	}
	sig, ok := t.Underlying().(*types.Signature)
	if !ok {
		return errors.New("target does not have an underlying function signature")
	}
	f.addTypesForMethod(sig)
	method, err := methodForSignature(sig, f.TargetName, f.Imports)
	if err != nil {
		return f.loadError(err)
	}
	f.Function = method
	return nil
}
