package generator

import (
	"errors"
	"fmt"
	"go/build"
	"go/types"
	"log"
	"path/filepath"
	"reflect"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

func (f *Fake) loadPackages(c Cacher, workingDir string) error {
	log.Println("loading packages...")
	p, ok := c.Load(f.TargetPackage)
	if ok {
		f.Packages = p
		log.Printf("loaded %v packages from cache\n", len(f.Packages))
		return nil
	}
	importPath := f.TargetPackage
	if !filepath.IsAbs(importPath) {
		ctx := getBuildContext(workingDir)
		bp, err := ctx.Import(f.TargetPackage, workingDir, build.FindOnly)
		if err != nil {
			return err
		}
		importPath = bp.ImportPath
	}
	p, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedTypes | packages.NeedSyntax,
		Dir:   workingDir,
		Tests: true,
	}, importPath)
	if err != nil {
		return err
	}
	for i := range p {
		for j := range p[i].Errors {
			e := p[i].Errors[j]
			if isBuildTranscript(e) {
				// go list -export reports a package that failed to
				// compile as a single error holding the compiler's
				// output. go/packages then type-checks that package
				// from source, and those positioned errors are the
				// ones we act on.
				log.Printf("ignoring build failure, package was type-checked from source: %v", strings.TrimPrefix(fmt.Sprintf("%v", e), "-: "))
				continue
			}
			log.Printf("error loading packages: %v", strings.TrimPrefix(fmt.Sprintf("%v", e), "-: "))
			if i != 0 {
				continue
			}
			// A file that does not parse could hide part of the target,
			// so that has to be fixed first. Anything else, such as an
			// import that cannot be resolved or a type error elsewhere
			// in the package, only matters if the target's own
			// signatures turn out to depend on it.
			if e.Kind == packages.ParseError {
				if err == nil {
					err = e
				}
				continue
			}
			f.loadErrors = append(f.loadErrors, e)
		}
	}
	if err != nil {
		return err
	}
	f.Packages = p
	c.Store(f.TargetPackage, p)
	log.Printf("loaded %v packages\n", len(f.Packages))
	return nil
}

func (f *Fake) findPackage() error {
	var target *types.TypeName
	var pkg *packages.Package
	for i := range f.Packages {
		if f.Packages[i].Types == nil || f.Packages[i].Types.Scope() == nil {
			continue
		}
		pkg = f.Packages[i]
		if f.Mode == Package {
			break
		}

		raw := pkg.Types.Scope().Lookup(f.TargetName)
		if raw != nil {
			if typeName, ok := raw.(*types.TypeName); ok {
				target = typeName
				break
			}
		}
		pkg = nil
	}
	if pkg == nil {
		switch f.Mode {
		case Package:
			return fmt.Errorf("cannot find package with name: %s", f.TargetPackage)
		case InterfaceOrFunction:
			return fmt.Errorf("cannot find package with target: %s", f.TargetName)
		}
	}
	f.Target = target
	f.Package = pkg
	f.TargetPackage = imports.VendorlessPath(pkg.PkgPath)
	// The fake joins whatever package already lives in the destination
	// directory, whose name is not always the directory name.
	inDir := sameDir(f.DestinationDir, packageDir(pkg))
	if inDir {
		f.DestinationPackage = pkg.Name
	} else {
		f.DestinationPackage = destinationPackageName(f.DestinationDir, f.DestinationPackage)
	}
	if f.testPackage {
		f.DestinationPackage = strings.TrimSuffix(f.DestinationPackage, "_test") + "_test"
	}
	f.inTargetPackage = inDir && f.DestinationPackage == pkg.Name
	if !f.inTargetPackage {
		t := f.Imports.Add(pkg.Name, f.TargetPackage)
		f.TargetAlias = t.Alias
	}
	if f.Mode != Package {
		f.TargetName = target.Name()
		if f.testPackage && inDir && !isExported(f.TargetName) {
			return fmt.Errorf("cannot generate a fake for %s in package %s because it is unexported", f.TargetName, f.DestinationPackage)
		}
		if f.inTargetPackage && !isExported(f.TargetName) && !f.explicitName {
			// An unexported interface can only be faked from inside its
			// package, and a fake named after it should not become part
			// of the package's API.
			f.Name = unexport(f.Name)
		}
	}
	f.loadGenericTypeParams()

	if f.Mode == InterfaceOrFunction {
		if !f.IsInterface() && !f.IsFunction() {
			return fmt.Errorf("cannot generate a fake for %s because it is not an interface or function", f.TargetName)
		}

		if f.IsConstraintInterface() {
			return fmt.Errorf("cannot generate a fake for %s because it is a constraint interface (contains type constraints like ~string) which cannot be implemented by concrete types", f.TargetName)
		}
	}

	if f.IsInterface() {
		log.Printf("Found interface with name: [%s]\n", f.TargetName)
	}
	if f.IsFunction() {
		log.Printf("Found function with name: [%s]\n", f.TargetName)
	}
	if f.Mode == Package {
		log.Printf("Found package with name: [%s]\n", f.TargetPackage)
	}
	return nil
}

// isBuildTranscript reports whether a package loading error is the
// compiler output that go list -export attaches to a package it could not
// build, as opposed to a positioned error about a specific file.
func isBuildTranscript(e packages.Error) bool {
	return e.Kind == packages.ListError && e.Pos == "" && strings.HasPrefix(e.Msg, "# ")
}

// loadError explains why the target cannot be faked, followed by the
// package loading errors that were tolerated up to that point, since those
// are what left a type unresolved.
func (f *Fake) loadError(cause error) error {
	target := f.TargetName
	if f.Mode == Package {
		target = f.TargetPackage
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cannot generate a fake for %s: %v", target, cause)
	for _, e := range f.loadErrors {
		b.WriteString("\n  ")
		b.WriteString(strings.TrimPrefix(e.Error(), "-: "))
	}
	return errors.New(b.String())
}

// hasInvalidType reports whether typ, as it will be printed in the fake,
// mentions a type the loader could not resolve. Named types and aliases
// print by name, so only their type arguments are inspected.
func hasInvalidType(typ types.Type) bool {
	switch t := typ.(type) {
	case nil:
		return false
	case *types.Basic:
		return t.Kind() == types.Invalid
	case *types.Pointer:
		return hasInvalidType(t.Elem())
	case *types.Slice:
		return hasInvalidType(t.Elem())
	case *types.Array:
		return hasInvalidType(t.Elem())
	case *types.Chan:
		return hasInvalidType(t.Elem())
	case *types.Map:
		return hasInvalidType(t.Key()) || hasInvalidType(t.Elem())
	case *types.Named:
		return hasInvalidTypeArgs(t.TypeArgs())
	case *types.Alias:
		return hasInvalidTypeArgs(t.TypeArgs())
	case *types.Union:
		for i := 0; i < t.Len(); i++ {
			if hasInvalidType(t.Term(i).Type()) {
				return true
			}
		}
	case *types.Interface:
		for i := 0; i < t.NumEmbeddeds(); i++ {
			if hasInvalidType(t.EmbeddedType(i)) {
				return true
			}
		}
		for i := 0; i < t.NumExplicitMethods(); i++ {
			if hasInvalidType(t.ExplicitMethod(i).Type()) {
				return true
			}
		}
	case *types.Signature:
		return hasInvalidTuple(t.Params()) || hasInvalidTuple(t.Results())
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if hasInvalidType(t.Field(i).Type()) {
				return true
			}
		}
	}
	return false
}

func hasInvalidTypeArgs(args *types.TypeList) bool {
	for i := 0; i < args.Len(); i++ {
		if hasInvalidType(args.At(i)) {
			return true
		}
	}
	return false
}

func hasInvalidTuple(tuple *types.Tuple) bool {
	for i := 0; i < tuple.Len(); i++ {
		if hasInvalidType(tuple.At(i).Type()) {
			return true
		}
	}
	return false
}

// hasInvalidEmbed reports whether an interface, directly or through the
// interfaces it embeds, embeds a type the loader could not resolve. The
// type checker drops such an embed from the method set, which would leave
// the fake silently incomplete.
func hasInvalidEmbed(iface *types.Interface, seen map[*types.Interface]bool) bool {
	if seen[iface] {
		return false
	}
	seen[iface] = true
	for i := 0; i < iface.NumEmbeddeds(); i++ {
		embedded := iface.EmbeddedType(i)
		if hasInvalidType(embedded) {
			return true
		}
		if nested, ok := embedded.Underlying().(*types.Interface); ok && hasInvalidEmbed(nested, seen) {
			return true
		}
	}
	return false
}

// destinationPackageName returns the name of the package whose files are in
// dir, which is not always the directory's name. Only the package clauses are
// read. It falls back to the given name when dir holds no Go package, or
// when the files there disagree about the package name.
func destinationPackageName(dir, fallback string) string {
	if dir == "" {
		return fallback
	}
	bp, err := build.Default.ImportDir(dir, 0)
	if err != nil || bp.Name == "" {
		return fallback
	}
	return bp.Name
}

// packageDir returns the directory holding the package's source files.
func packageDir(pkg *packages.Package) string {
	if pkg.Dir != "" {
		return pkg.Dir
	}
	if len(pkg.GoFiles) > 0 {
		return filepath.Dir(pkg.GoFiles[0])
	}
	return ""
}

// sameDir reports whether a and b name the same directory. Either being
// empty means "unknown", which never matches.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return canonicalDir(a) == canonicalDir(b)
}

func canonicalDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Clean(dir)
}

// loadGenericTypeParams records the type parameter list of a generic
// interface target, in both the declaration form ("[T pkg.Constraint]") and
// the instantiation form ("[T]"). Constraints are rendered with the same
// qualifier as method signatures, so any package they refer to is imported
// and aliased consistently. It must run after the target package has been
// added to f.Imports.
func (f *Fake) loadGenericTypeParams() {
	if f.Target == nil {
		return
	}
	named, ok := f.Target.Type().(*types.Named)
	if !ok {
		return
	}
	if _, ok := named.Underlying().(*types.Interface); !ok {
		return
	}
	typeParams := named.TypeParams()
	if typeParams.Len() == 0 {
		return
	}
	names := make([]string, 0, typeParams.Len())
	namesAndConstraints := make([]string, 0, typeParams.Len())
	for i := 0; i < typeParams.Len(); i++ {
		param := typeParams.At(i)
		f.addImportsFor(param.Constraint())
		constraint := types.TypeString(param.Constraint(), f.Imports.AliasForPackage)
		names = append(names, param.Obj().Name())
		namesAndConstraints = append(namesAndConstraints, param.Obj().Name()+" "+constraint)
	}
	f.GenericTypeParameters = "[" + strings.Join(names, ", ") + "]"
	f.GenericTypeParametersAndConstraints = "[" + strings.Join(namesAndConstraints, ", ") + "]"
}

// addImportsFor inspects the given type and adds imports to the fake if importable
// types are found.
func (f *Fake) addImportsFor(typ types.Type) {
	if typ == nil {
		return
	}

	switch t := typ.(type) {
	case *types.Basic:
		return
	case *types.Pointer:
		f.addImportsFor(t.Elem())
	case *types.Map:
		f.addImportsFor(t.Key())
		f.addImportsFor(t.Elem())
	case *types.Chan:
		f.addImportsFor(t.Elem())
	case *types.Alias:
		f.addImportsForNamedType(t)
	case *types.Named:
		f.addImportsForNamedType(t)
	case *types.Slice:
		f.addImportsFor(t.Elem())
	case *types.Array:
		f.addImportsFor(t.Elem())
	case *types.TypeParam:
		return
	case *types.Union:
		for i := 0; i < t.Len(); i++ {
			f.addImportsFor(t.Term(i).Type())
		}
	case *types.Interface:
		for i := 0; i < t.NumEmbeddeds(); i++ {
			f.addImportsFor(t.EmbeddedType(i))
		}
		for i := 0; i < t.NumExplicitMethods(); i++ {
			f.addImportsFor(t.ExplicitMethod(i).Type())
		}
	case *types.Signature:
		f.addTypesForMethod(t)
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			f.addImportsFor(t.Field(i).Type())
		}
	default:
		log.Printf("!!! WARNING: Missing case for type %s\n", reflect.TypeOf(typ).String())
	}
}

func (f *Fake) addImportsForNamedType(t interface {
	Obj() *types.TypeName
	TypeArgs() *types.TypeList
}) {
	if t.Obj() != nil && t.Obj().Pkg() != nil {
		typeArgs := t.TypeArgs()
		for i := 0; i < typeArgs.Len(); i++ {
			f.addImportsFor(typeArgs.At(i))
		}
		if f.inTargetPackage && imports.VendorlessPath(t.Obj().Pkg().Path()) == f.TargetPackage {
			return
		}
		f.Imports.Add(t.Obj().Pkg().Name(), t.Obj().Pkg().Path())
	}
}
