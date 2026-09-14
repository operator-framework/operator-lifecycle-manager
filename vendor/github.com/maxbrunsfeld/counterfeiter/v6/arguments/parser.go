package arguments

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"unicode"
)

// Option configures New.
type Option func(*options)

type options struct {
	defaults *ParsedArguments
}

// WithDefaults supplies the arguments parsed from a "-generate" invocation.
// Flags that a directive does not set itself fall back to the values given
// there, so that -o, -fake-name-template, -header and -q can be configured
// once per package on the "//go:generate ... -generate" line.
func WithDefaults(defaults *ParsedArguments) Option {
	return func(o *options) {
		o.defaults = defaults
	}
}

func New(args []string, workingDir string, evaler Evaler, stater Stater, opts ...Option) (*ParsedArguments, error) {
	if len(args) == 0 {
		return nil, errors.New("argument parsing requires at least one argument")
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	defaults := o.defaults
	if defaults == nil {
		defaults = &ParsedArguments{}
	}

	fs := flag.NewFlagSet("counterfeiter", flag.ContinueOnError)
	fakeNameFlag := fs.String(
		"fake-name",
		"",
		"The name of the fake struct",
	)
	fakeNameTemplateFlag := fs.String(
		"fake-name-template",
		"",
		"A text/template for the name of the fake struct, evaluated against {{.TargetName}}",
	)

	outputPathFlag := fs.String(
		"o",
		"",
		"The file or directory to which the generated fake will be written",
	)

	packageFlag := fs.Bool(
		"p",
		false,
		"Whether or not to generate a package shim",
	)
	generateFlag := fs.Bool(
		"generate",
		false,
		"Identify all //counterfeiter:generate directives in the current working directory and generate fakes for them",
	)
	headerFlag := fs.String(
		"header",
		"",
		"A path to a file that should be used as a header for the generated fake",
	)
	quietFlag := fs.Bool(
		"q",
		false,
		"Suppress status statements",
	)
	testFlag := fs.Bool(
		"test",
		false,
		"Generate the fake into the external test package (<package>_test) of the output directory",
	)
	helpFlag := fs.Bool(
		"help",
		false,
		"Display this help",
	)

	err := fs.Parse(args[1:])
	if err != nil {
		return nil, err
	}
	if *helpFlag {
		return nil, errors.New(usage)
	}
	if len(fs.Args()) == 0 && !*generateFlag {
		return nil, errors.New(usage)
	}

	fakeNameTemplateText := or(*fakeNameTemplateFlag, defaults.FakeNameTemplate)
	fakeNameTemplate, err := parseFakeNameTemplate(fakeNameTemplateText)
	if err != nil {
		return nil, err
	}
	outputPath := or(*outputPathFlag, defaults.OutputPath)

	packageMode := *packageFlag
	testPackage := *testFlag || defaults.TestPackage
	if packageMode && testPackage {
		return nil, errors.New("-test cannot be combined with -p: a package shim is not a test package")
	}
	result := &ParsedArguments{
		PrintToStdOut: any(args, "-"),
		GenerateInterfaceAndShimFromPackageDirectory: packageMode,
		GenerateMode: *generateFlag,
		HeaderFile:   or(*headerFlag, defaults.HeaderFile),
		Quiet:        *quietFlag || defaults.Quiet,
		TestPackage:  testPackage,
	}
	if *generateFlag {
		// Keep the raw flag values: they become the defaults for every directive.
		result.OutputPath = outputPath
		result.FakeNameTemplate = fakeNameTemplateText
		return result, nil
	}
	// "-" only asks for stdout; it is not a source path or an interface.
	positional := without(fs.Args(), "-")
	err = result.parseSourcePackageDir(packageMode, workingDir, evaler, stater, positional)
	if err != nil {
		return nil, err
	}
	result.parseInterfaceName(packageMode, positional)
	err = result.parseFakeName(packageMode, *fakeNameFlag, fakeNameTemplate, positional)
	if err != nil {
		return nil, err
	}
	err = result.parseOutputPath(packageMode, workingDir, outputPath, positional)
	if err != nil {
		return nil, err
	}
	result.parseDestinationPackageName(packageMode, positional)
	result.parsePackagePath(packageMode, positional)
	return result, nil
}

func or(opts ...string) string {
	for _, s := range opts {
		if s != "" {
			return s
		}
	}
	return ""
}

// fakeNameTemplate is a parsed -fake-name-template; an empty flag parses as
// the default name, "Fake" followed by the target name.
type fakeNameTemplate struct {
	text string
	tmpl *template.Template
}

func parseFakeNameTemplate(text string) (fakeNameTemplate, error) {
	if text == "" {
		text = "Fake{{.TargetName}}"
	}
	tmpl, err := template.New("fake-name-template").Parse(text)
	if err != nil {
		return fakeNameTemplate{}, fmt.Errorf("invalid -fake-name-template %q: %w", text, err)
	}
	return fakeNameTemplate{text: text, tmpl: tmpl}, nil
}

func (t fakeNameTemplate) render(targetName string) (string, error) {
	var b strings.Builder
	err := t.tmpl.Execute(&b, struct{ TargetName string }{targetName})
	if err != nil {
		return "", fmt.Errorf("invalid -fake-name-template %q: %w", t.text, err)
	}
	return b.String(), nil
}

func (a *ParsedArguments) PrettyPrint() {
	b, _ := json.MarshalIndent(a, "", " ")
	fmt.Println(string(b))
}

func (a *ParsedArguments) parseInterfaceName(packageMode bool, args []string) {
	if packageMode {
		a.InterfaceName = ""
		return
	}
	if len(args) == 1 {
		fullyQualifiedInterface := strings.Split(args[0], ".")
		a.InterfaceName = fullyQualifiedInterface[len(fullyQualifiedInterface)-1]
	} else {
		a.InterfaceName = args[1]
	}
}

func (a *ParsedArguments) parseSourcePackageDir(packageMode bool, workingDir string, evaler Evaler, stater Stater, args []string) error {
	if packageMode {
		a.SourcePackageDir = args[0]
		return nil
	}
	if len(args) <= 1 {
		return nil
	}
	s, err := getSourceDir(args[0], workingDir, evaler, stater)
	if err != nil {
		return err
	}
	a.SourcePackageDir = s
	return nil
}

func (a *ParsedArguments) parseFakeName(packageMode bool, fakeName string, tmpl fakeNameTemplate, args []string) error {
	if packageMode {
		a.parsePackagePath(packageMode, args)
		a.FakeImplName = strings.ToUpper(path.Base(a.PackagePath))[:1] + path.Base(a.PackagePath)[1:]
		return nil
	}
	if fakeName != "" {
		a.FakeImplName = fakeName
		a.FakeNameExplicit = true
		return nil
	}
	var err error
	a.FakeImplName, err = tmpl.render(fixupUnexportedNames(a.InterfaceName))
	return err
}

func (a *ParsedArguments) parseOutputPath(packageMode bool, workingDir string, outputPath string, args []string) error {
	snakeCaseName := strings.ToLower(camelRegexp.ReplaceAllString(a.FakeImplName, "${1}_${2}"))
	fileName := snakeCaseName + ".go"
	if a.TestPackage {
		fileName = snakeCaseName + "_test.go"
	}

	if outputPath != "" {
		if !filepath.IsAbs(outputPath) {
			outputPath = filepath.Join(workingDir, outputPath)
		}
		a.OutputPath = outputPath
		if !strings.HasSuffix(outputPath, ".go") {
			a.OutputPath = filepath.Join(outputPath, fileName)
		} else if a.TestPackage && !strings.HasSuffix(outputPath, "_test.go") {
			return fmt.Errorf("-test generates a _test package, which Go only compiles from a _test.go file, not %s", filepath.Base(outputPath))
		}
		return nil
	}

	if packageMode {
		a.parseDestinationPackageName(packageMode, args)
		a.OutputPath = path.Join(workingDir, a.DestinationPackageName, fileName)
		return nil
	}

	if a.TestPackage {
		// The fake belongs to the tests in the directory counterfeiter runs
		// in, wherever the interface comes from.
		a.OutputPath = filepath.Join(workingDir, fileName)
		return nil
	}
	d := workingDir
	if len(args) > 1 {
		d = a.SourcePackageDir
	}
	a.OutputPath = filepath.Join(d, packageNameForPath(d), fileName)
	return nil
}

func (a *ParsedArguments) parseDestinationPackageName(packageMode bool, args []string) {
	if packageMode {
		a.parsePackagePath(packageMode, args)
		a.DestinationPackageName = path.Base(a.PackagePath) + "shim"
		return
	}

	a.DestinationPackageName = restrictToValidPackageName(filepath.Base(filepath.Dir(a.OutputPath)))
	if a.TestPackage {
		a.DestinationPackageName += "_test"
	}
}

func (a *ParsedArguments) parsePackagePath(packageMode bool, args []string) {
	if packageMode {
		a.PackagePath = args[0]
		return
	}
	if len(args) == 1 {
		fullyQualifiedInterface := strings.Split(args[0], ".")
		a.PackagePath = strings.Join(fullyQualifiedInterface[:len(fullyQualifiedInterface)-1], ".")
	} else {
		a.InterfaceName = args[1]
	}

	if a.PackagePath == "" {
		a.PackagePath = a.SourcePackageDir
	}
}

type ParsedArguments struct {
	GenerateInterfaceAndShimFromPackageDirectory bool

	SourcePackageDir string // abs path to the dir containing the interface to fake
	PackagePath      string // package path to the package containing the interface to fake
	OutputPath       string // path to write the fake file to

	DestinationPackageName string // often the base-dir for OutputPath but must be a valid package name

	InterfaceName    string // the interface to counterfeit
	FakeImplName     string // the name of the struct implementing the given interface
	FakeNameExplicit bool   // FakeImplName came from -fake-name and is used as given

	PrintToStdOut bool
	GenerateMode  bool
	Quiet         bool
	TestPackage   bool // write the fake into the external test package (<package>_test) of its directory

	HeaderFile       string
	FakeNameTemplate string // text/template for FakeImplName, evaluated against {{.TargetName}}
}

func fixupUnexportedNames(interfaceName string) string {
	asRunes := []rune(interfaceName)
	if len(asRunes) == 0 || !unicode.IsLower(asRunes[0]) {
		return interfaceName
	}
	asRunes[0] = unicode.ToUpper(asRunes[0])
	return string(asRunes)
}

var camelRegexp = regexp.MustCompile("([a-z])([A-Z])")

func packageNameForPath(pathToPackage string) string {
	_, packageName := filepath.Split(pathToPackage)
	return packageName + "fakes"
}

func getSourceDir(path string, workingDir string, evaler Evaler, stater Stater) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}

	evaluatedPath, err := evaler(path)
	if err != nil {
		return "", fmt.Errorf("No such file/directory/package [%s]: %v", path, err)
	}

	stat, err := stater(evaluatedPath)
	if err != nil {
		return "", fmt.Errorf("No such file/directory/package [%s]: %v", path, err)
	}

	if !stat.IsDir() {
		return filepath.Dir(path), nil
	}
	return path, nil
}

func without(slice []string, needle string) []string {
	result := make([]string, 0, len(slice))
	for _, str := range slice {
		if str != needle {
			result = append(result, str)
		}
	}
	return result
}

func any(slice []string, needle string) bool {
	for _, str := range slice {
		if str == needle {
			return true
		}
	}

	return false
}

func restrictToValidPackageName(input string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		} else {
			return -1
		}
	}, input)
}
