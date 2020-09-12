package astgen

import (
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
)

func buildType(t reflect.Type) (ast.Expr, error) {
	if t.Name() != "" {
		return &ast.Ident{Name: t.Name()}, nil
	}
	switch t.Kind() {
	case reflect.Interface:
		return &ast.InterfaceType{Methods: &ast.FieldList{}}, nil
	case reflect.Array:
		elem, err := buildType(t.Elem())
		if err != nil {
			return nil, err
		}
		return &ast.ArrayType{
			Len: &ast.BasicLit{Kind: token.INT, Value: fmt.Sprint(t.Len())},
			Elt: elem,
		}, nil
	case reflect.Slice:
		elem, err := buildType(t.Elem())
		if err != nil {
			return nil, err
		}
		return &ast.ArrayType{Elt: elem}, nil
	case reflect.Map:
		k, err := buildType(t.Key())
		if err != nil {
			return nil, err
		}
		v, err := buildType(t.Elem())
		if err != nil {
			return nil, err
		}
		return &ast.MapType{Key: k, Value: v}, nil
	case reflect.Struct:
		if t.Name() != "" {
			return &ast.Ident{Name: t.Name()}, nil
		}
		fs := make([]*ast.Field, 0, t.NumField())
		var prevType ast.Expr
		var prevTag reflect.StructTag
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			t, err := buildType(sf.Type)
			if err != nil {
				return nil, err
			}
			if reflect.DeepEqual(prevType, t) && prevTag == sf.Tag {
				fs[len(fs)-1].Names = append(fs[len(fs)-1].Names, &ast.Ident{Name: sf.Name})
				continue
			}
			var tag *ast.BasicLit
			if sf.Tag != "" {
				tag = &ast.BasicLit{Value: "`" + string(sf.Tag) + "`"}
			}
			fs = append(fs, &ast.Field{
				Names: []*ast.Ident{&ast.Ident{Name: sf.Name}},
				Type:  t,
				Tag:   tag,
			})
			prevType, prevTag = t, sf.Tag
		}
		return &ast.StructType{Fields: &ast.FieldList{List: fs}}, nil
	case reflect.Ptr:
		t, err := buildType(t.Elem())
		if err != nil {
			return nil, err
		}
		return &ast.StarExpr{X: t}, nil
	default:
		return nil, &unexpectedTypeError{t}
	}
}
