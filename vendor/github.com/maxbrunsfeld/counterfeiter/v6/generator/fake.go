package generator

import (
	"bytes"
	"errors"
	"go/types"
	"log"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

// FakeMode indicates the type of Fake to generate.
type FakeMode int

// FakeMode can be Interface, Function, or Package.
const (
	InterfaceOrFunction FakeMode = iota
	Package
)

// Fake is used to generate a Fake implementation of an interface.
type Fake struct {
	Packages                            []*packages.Package
	Package                             *packages.Package
	Target                              *types.TypeName
	Mode                                FakeMode
	DestinationPackage                  string
	DestinationDir                      string
	Name                                string
	GenericTypeParametersAndConstraints string
	GenericTypeParameters               string
	TargetAlias                         string
	TargetName                          string
	TargetPackage                       string
	Imports                             Imports
	Methods                             []Method
	Function                            Method
	Header                              string

	// loadErrors holds the errors go/packages reported for the target
	// package that did not stop loading, kept so a failure caused by one
	// of them can say what went wrong.
	loadErrors []packages.Error

	// inTargetPackage is true when the fake is written into the directory of
	// the package that declares the target, so that package must not be
	// imported and its names are used unqualified.
	inTargetPackage bool

	// testPackage is true when the fake belongs to the external test package
	// ("<package>_test") of the destination directory.
	testPackage bool

	// explicitName is true when the user chose the fake's name, so it is
	// used as given.
	explicitName bool
}

// Method is a method of the interface.
type Method struct {
	Name    string
	Params  Params
	Returns Returns
}

// Option configures a Fake before its packages are loaded.
type Option func(*Fake)

// WithDestinationDir records the directory the fake will be written to. When
// that is the directory of the target's own package, the fake is generated as
// a member of that package: it omits the self-import and leaves the package's
// types unqualified.
func WithDestinationDir(dir string) Option {
	return func(f *Fake) {
		f.DestinationDir = dir
	}
}

// WithTestPackage generates the fake into the external test package of the
// destination directory: the "<package>_test" package that go test compiles
// next to the package living there. That package is distinct from the target's
// own package even when the directory is the same, so the target is imported
// and must be exported.
func WithTestPackage() Option {
	return func(f *Fake) {
		f.testPackage = true
	}
}

// WithExplicitName records that the fake's name was chosen by the user rather
// than derived from the target's name. The name is then used as given; in
// particular it stays exported for an unexported target in its own package.
func WithExplicitName() Option {
	return func(f *Fake) {
		f.explicitName = true
	}
}

// NewFake returns a Fake that loads the package and finds the interface or the
// function.
func NewFake(fakeMode FakeMode, targetName string, packagePath string, fakeName string, destinationPackage string, headerContent string, workingDir string, cache Cacher, opts ...Option) (*Fake, error) {
	f := &Fake{
		TargetName:         targetName,
		TargetPackage:      packagePath,
		Name:               fakeName,
		Mode:               fakeMode,
		DestinationPackage: destinationPackage,
		Imports:            newImports(),
		Header:             headerContent,
	}
	for _, opt := range opts {
		opt(f)
	}

	f.Imports.Add("sync", "sync")
	err := f.loadPackages(cache, workingDir)
	if err != nil {
		return nil, err
	}

	// TODO: Package mode here
	err = f.findPackage()
	if err != nil {
		return nil, err
	}

	if f.IsInterface() || f.Mode == Package {
		err = f.loadMethods()
		if err != nil {
			return nil, err
		}
	}
	if f.IsFunction() {
		err = f.loadMethodForFunction()
		if err != nil {
			return nil, err
		}
	}
	return f, nil
}

// IsInterface indicates whether the fake is for an interface.
func (f *Fake) IsInterface() bool {
	if f.Target == nil || f.Target.Type() == nil {
		return false
	}
	return types.IsInterface(f.Target.Type())
}

// IsFunction indicates whether the fake is for a function..
func (f *Fake) IsFunction() bool {
	if f.Target == nil || f.Target.Type() == nil || f.Target.Type().Underlying() == nil {
		return false
	}
	_, ok := f.Target.Type().Underlying().(*types.Signature)
	return ok
}

// IsConstraintInterface indicates whether the interface is a constraint interface
// (contains type constraints like ~string) which cannot be implemented by concrete types.
func (f *Fake) IsConstraintInterface() bool {
	if !f.IsInterface() {
		return false
	}

	iface, ok := f.Target.Type().Underlying().(*types.Interface)
	if !ok {
		return false
	}

	// check if the interface has any type constraints
	for i := 0; i < iface.NumEmbeddeds(); i++ {
		if _, ok := iface.EmbeddedType(i).(*types.Union); ok {
			return true
		}
	}

	// check for approximation constraints by examining the string representation
	// a bit of a hack, but the Go types API doesn't expose type constraints cleanly
	return strings.Contains(iface.String(), "~")
}

func unexport(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

func isExported(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

// Generate uses the Fake to generate an implementation, optionally running
// goimports on the output.
func (f *Fake) Generate(runImports bool) ([]byte, error) {
	var tmpl *template.Template
	if f.IsInterface() {
		log.Printf("Writing fake %s for interface %s to package %s\n", f.Name, f.TargetName, f.DestinationPackage)
		tmpl = template.Must(template.New("fake").Funcs(interfaceFuncs).Parse(interfaceTemplate))
	}
	if f.IsFunction() {
		log.Printf("Writing fake %s for function %s to package %s\n", f.Name, f.TargetName, f.DestinationPackage)
		tmpl = template.Must(template.New("fake").Funcs(functionFuncs).Parse(functionTemplate))
	}
	if f.Mode == Package {
		log.Printf("Writing fake %s for package %s to package %s\n", f.Name, f.TargetPackage, f.DestinationPackage)
		tmpl = template.Must(template.New("fake").Funcs(packageFuncs).Parse(packageTemplate))
	}
	if tmpl == nil {
		return nil, errors.New("counterfeiter can only generate fakes for interfaces or specific functions")
	}

	b := &bytes.Buffer{}
	err := tmpl.Execute(b, f)
	if err != nil {
		return nil, err
	}
	if runImports {
		return imports.Process("counterfeiter_temp_process_file", b.Bytes(), nil)
	}
	return b.Bytes(), nil
}
