package astgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Build ast from interface{}.
func Build(x interface{}) (ast.Node, error) {
	return (&builder{}).build(reflect.ValueOf(x))
}

type builder struct {
	vars []builderVar
}

type builderVar struct {
	name   string
	typ    ast.Expr
	expr   ast.Expr
	varptr bool
}

func (b *builder) build(v reflect.Value) (ast.Node, error) {
	n, err := b.buildInner(v)
	if err != nil {
		return nil, err
	}
	if len(b.vars) == 0 {
		return n, nil
	}
	t, err := buildType(v.Type())
	if err != nil {
		return nil, err
	}
	params := make([]*ast.Field, 0, len(b.vars))
	args := make([]ast.Expr, 0, len(b.vars))
	body := make([]ast.Stmt, 0, len(b.vars))
	var prevType ast.Expr
	for i, bv := range b.vars {
		if bv.varptr {
			body = append(body, &ast.AssignStmt{
				Tok: token.DEFINE,
				Lhs: []ast.Expr{&ast.Ident{Name: bv.name}},
				Rhs: []ast.Expr{bv.expr},
			})
			continue
		}
		args = append(args, bv.expr)
		if i > 0 && reflect.DeepEqual(prevType, bv.typ) {
			params[len(params)-1].Names = append(
				params[len(params)-1].Names,
				&ast.Ident{Name: bv.name},
			)
			continue
		}
		prevType = bv.typ
		params = append(params, &ast.Field{
			Names: []*ast.Ident{&ast.Ident{Name: bv.name}},
			Type:  bv.typ,
		})
	}
	return &ast.CallExpr{
		Fun: &ast.ParenExpr{
			X: &ast.FuncLit{
				Type: &ast.FuncType{
					Params: &ast.FieldList{List: params},
					Results: &ast.FieldList{
						List: []*ast.Field{
							&ast.Field{Type: t},
						},
					},
				},
				Body: &ast.BlockStmt{
					List: append(body, &ast.ReturnStmt{Results: []ast.Expr{n}}),
				},
			},
		},
		Args: args,
	}, nil
}

func (b *builder) buildInner(v reflect.Value) (ast.Expr, error) {
	switch v.Kind() {
	case reflect.Invalid:
		return &ast.Ident{Name: "nil"}, nil
	case reflect.Bool:
		if v.Bool() {
			return &ast.Ident{Name: "true"}, nil
		}
		return &ast.Ident{Name: "false"}, nil
	case reflect.Int:
		return &ast.BasicLit{Kind: token.INT, Value: fmt.Sprint(v.Int())}, nil
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return callExpr(token.INT, v.Type().Name(), fmt.Sprint(v.Int())), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return callExpr(token.INT, v.Type().Name(), fmt.Sprint(v.Uint())), nil
	case reflect.Float32:
		return callExpr(token.FLOAT, "float32", fmt.Sprint(v.Float())), nil
	case reflect.Float64:
		s := fmt.Sprint(v.Float())
		if !strings.ContainsRune(s, '.') {
			s += ".0"
		}
		return &ast.BasicLit{Kind: token.FLOAT, Value: s}, nil
	case reflect.Complex64, reflect.Complex128:
		return callExpr(token.FLOAT, v.Type().Name(), fmt.Sprint(v.Complex())), nil
	case reflect.String:
		if strings.ContainsRune(v.String(), '"') && !strings.ContainsRune(v.String(), '`') {
			s := strings.Replace(v.String(), `"`, "", -1)
			if len(strconv.Quote(s)) == len(s)+2 { // check no escape characters
				return &ast.BasicLit{Kind: token.STRING, Value: "`" + v.String() + "`"}, nil
			}
		}
		return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(v.String())}, nil
	case reflect.Interface:
		e, err := b.buildExpr(v.Elem())
		if err != nil {
			return nil, err
		}
		t, err := buildType(v.Type())
		if err != nil {
			return nil, err
		}
		return &ast.CallExpr{Fun: t, Args: []ast.Expr{e}}, nil
	case reflect.Array, reflect.Slice:
		exprs := make([]ast.Expr, v.Len())
		for i := 0; i < v.Len(); i++ {
			w, err := b.buildExpr(v.Index(i))
			if err != nil {
				return nil, err
			}
			exprs[i] = w
		}
		t, err := buildType(v.Type())
		if err != nil {
			return nil, err
		}
		return &ast.CompositeLit{Type: t, Elts: exprs}, nil
	case reflect.Map:
		keys := make([]struct {
			value reflect.Value
			expr  ast.Expr
			str   string
		}, v.Len())
		for i, key := range v.MapKeys() {
			expr, err := b.buildExpr(key)
			if err != nil {
				return nil, err
			}
			var buf bytes.Buffer
			printer.Fprint(&buf, token.NewFileSet(), expr)
			keys[i] = struct {
				value reflect.Value
				expr  ast.Expr
				str   string
			}{value: key, expr: expr, str: buf.String()}
		}
		sort.Slice(keys, func(i, j int) bool {
			return keys[i].str < keys[j].str
		})
		exprs := make([]ast.Expr, v.Len())
		for i, key := range keys {
			v, err := b.buildExpr(v.MapIndex(key.value))
			if err != nil {
				return nil, err
			}
			exprs[i] = &ast.KeyValueExpr{Key: key.expr, Value: v}
		}
		t, err := buildType(v.Type())
		if err != nil {
			return nil, err
		}
		return &ast.CompositeLit{Type: t, Elts: exprs}, nil
	case reflect.Struct:
		exprs := make([]ast.Expr, 0, v.NumField())
		for i := 0; i < v.NumField(); i++ {
			if isZero(v.Field(i)) {
				continue
			}
			k := &ast.Ident{Name: v.Type().Field(i).Name}
			v, err := b.buildExpr(v.Field(i))
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, &ast.KeyValueExpr{Key: k, Value: v})
		}
		t, err := buildType(v.Type())
		if err != nil {
			return nil, err
		}
		return &ast.CompositeLit{Type: t, Elts: exprs}, nil
	case reflect.Ptr:
		w, err := b.buildExpr(v.Elem())
		if err != nil {
			return nil, err
		}
		switch v.Elem().Kind() {
		case reflect.Invalid, reflect.Bool, reflect.String, reflect.Interface, reflect.Ptr,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
			return b.newPtrExpr(v.Elem(), w)
		}
		return &ast.UnaryExpr{Op: token.AND, X: w}, nil
	default:
		return nil, &unexpectedTypeError{v.Type()}
	}
}

type unexpectedTypeError struct{ t reflect.Type }

func (err *unexpectedTypeError) Error() string {
	return fmt.Sprintf("unexpected type: %s", err.t.Kind())
}

func callExpr(kind token.Token, name, value string) *ast.CallExpr {
	return &ast.CallExpr{
		Fun: &ast.Ident{Name: name},
		Args: []ast.Expr{
			&ast.BasicLit{Kind: kind, Value: value},
		},
	}
}

func (b *builder) buildExpr(v reflect.Value) (ast.Expr, error) {
	w, err := b.buildInner(v)
	if err != nil {
		return nil, err
	}
	e, ok := w.(ast.Expr)
	if !ok {
		return nil, fmt.Errorf("expected ast.Expr but got: %T", w)
	}
	return e, nil
}

func (b *builder) getVarName(v reflect.Value, t, e ast.Expr) string {
	for _, bv := range b.vars {
		if reflect.DeepEqual(t, bv.typ) && reflect.DeepEqual(e, bv.expr) {
			return bv.name
		}
	}
	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), e)
	base := strings.Map(func(r rune) rune {
		if '0' <= r && r <= '9' || 'A' <= r && r <= 'Z' || 'a' <= r && r <= 'z' {
			return r
		}
		return -1
	}, buf.String())
	typ := v.Type().Name()
	if typ == "" {
		var b bool
		typ = strings.Map(func(r rune) rune {
			if !b && ('0' <= r && r <= '9' || 'A' <= r && r <= 'Z' || 'a' <= r && r <= 'z') {
				return r
			}
			b = true
			return -1
		}, buf.String())
	}
	if len(typ) > 1 {
		base = strings.ReplaceAll(base, typ, typ[:1])
	}
	if len(base) == 0 || '0' <= base[0] && base[0] <= '9' {
		base = "x" + base
	}
	if len(base) > 3 {
		base = base[:3]
	}
	i := 1
	if len(base) < i {
		i = len(base)
	}
	name := base[:i]
	for {
		var found bool
		for _, bv := range b.vars {
			if bv.name == name {
				found = true
				break
			}
		}
		if !found {
			break
		}
		i++
		if i <= len(base) {
			name = base[:i]
		} else {
			name = base + strconv.Itoa(i-len(base))
		}
	}
	bv := builderVar{name: name, typ: t, expr: e, varptr: isIdentPtrExpr(e)}
	b.vars = append(b.vars, bv)
	return name
}

func (b *builder) newPtrExpr(v reflect.Value, e ast.Expr) (ast.Expr, error) {
	t, err := buildType(v.Type())
	if err != nil {
		return nil, err
	}
	return &ast.UnaryExpr{
		Op: token.AND,
		X:  &ast.Ident{Name: b.getVarName(v, t, e)},
	}, nil
}

func isIdentPtrExpr(e ast.Expr) bool {
	if e, ok := e.(*ast.UnaryExpr); ok {
		if e.Op == token.AND {
			_, ok := e.X.(*ast.Ident)
			return ok
		}
	}
	return false
}
