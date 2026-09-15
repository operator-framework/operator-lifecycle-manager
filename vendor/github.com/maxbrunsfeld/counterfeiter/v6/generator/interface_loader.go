package generator

import (
	"errors"
	"fmt"
	"go/types"

	"golang.org/x/tools/go/types/typeutil"
)

func (f *Fake) addTypesForMethod(sig *types.Signature) {
	for i := 0; i < sig.Results().Len(); i++ {
		ret := sig.Results().At(i)
		f.addImportsFor(ret.Type())
	}
	for i := 0; i < sig.Params().Len(); i++ {
		param := sig.Params().At(i)
		f.addImportsFor(param.Type())
	}
}

func methodForSignature(sig *types.Signature, methodName string, imports Imports) (Method, error) {
	if hasInvalidType(sig) {
		return Method{}, fmt.Errorf("method %s uses a type that could not be loaded", methodName)
	}
	params := []Param{}
	for i := 0; i < sig.Params().Len(); i++ {
		param := sig.Params().At(i)
		isVariadic := i == sig.Params().Len()-1 && sig.Variadic()
		typ := types.TypeString(param.Type(), imports.AliasForPackage)
		if isVariadic {
			typ = "..." + typ[2:] // Change []string to ...string
		}
		_, isSlice := param.Type().Underlying().(*types.Slice)
		p := Param{
			Name:       fmt.Sprintf("arg%v", i+1),
			Type:       typ,
			IsVariadic: isVariadic,
			IsSlice:    isSlice,
		}
		params = append(params, p)
	}
	returns := []Return{}
	for i := 0; i < sig.Results().Len(); i++ {
		ret := sig.Results().At(i)
		r := Return{
			Name: fmt.Sprintf("result%v", i+1),
			Type: types.TypeString(ret.Type(), imports.AliasForPackage),
		}
		returns = append(returns, r)
	}
	return Method{
		Name:    methodName,
		Returns: returns,
		Params:  params,
	}, nil
}

// interfaceMethodSet identifies the methods that are exported for a given
// interface.
func interfaceMethodSet(t types.Type) []*rawMethod {
	if t == nil {
		return nil
	}
	var result []*rawMethod
	methods := typeutil.IntuitiveMethodSet(t, nil)
	for i := range methods {
		if methods[i].Obj() == nil || methods[i].Type() == nil {
			continue
		}
		fun, ok := methods[i].Obj().(*types.Func)
		if !ok {
			continue
		}
		sig, ok := methods[i].Type().(*types.Signature)
		if !ok {
			continue
		}
		result = append(result, &rawMethod{
			Func:      fun,
			Signature: sig,
		})
	}

	return result
}

func (f *Fake) loadMethods() error {
	var methods []*rawMethod
	if f.Mode == Package {
		methods = packageMethodSet(f.Package)
	} else {
		if !f.IsInterface() || f.Target == nil || f.Target.Type() == nil {
			return nil
		}
		if iface, ok := f.Target.Type().Underlying().(*types.Interface); ok && hasInvalidEmbed(iface, map[*types.Interface]bool{}) {
			return f.loadError(errors.New("an embedded interface could not be loaded"))
		}
		methods = interfaceMethodSet(f.Target.Type())
	}

	for i := range methods {
		f.addTypesForMethod(methods[i].Signature)
	}

	for i := range methods {
		method, err := methodForSignature(methods[i].Signature, methods[i].Func.Name(), f.Imports)
		if err != nil {
			return f.loadError(err)
		}
		f.Methods = append(f.Methods, method)
	}
	return nil
}
