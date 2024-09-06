package constraints

import (
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/interpreter/functions"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"

	"github.com/blang/semver/v4"
)

// PropertiesKey is the key for bundle properties map (input data for CEL evaluation)
const PropertiesKey = "properties"

// Cel is a struct representing CEL expression information
type Cel struct {
	// The CEL expression
	Rule string `json:"rule" yaml:"rule"`
}

// NewCelEnvironment returns a CEL environment which can be used to
// evaluate CEL expression and an error if occurs
func NewCelEnvironment() *CelEnvironment {
	env, err := cel.NewEnv(cel.Declarations(
		decls.NewVar(PropertiesKey, decls.NewListType(decls.NewMapType(decls.String, decls.Any)))),
		cel.Lib(semverLib{}),
	)
	// If an error occurs here, it means the CEL environment is unable to load
	// configuration for custom libraries properly. Hence, the CEL environment is
	// unusable. Panic here will cause the program to fail immediately to prevent
	// cascading failures later on when this CEL env is in use.
	if err != nil {
		panic(err)
	}
	return &CelEnvironment{
		env: env,
	}
}

// CelEnvironment is a struct that encapsulates CEL custom program environment
type CelEnvironment struct {
	env *cel.Env
}

// CelProgram is a struct that encapsulates compiled CEL program
type CelProgram struct {
	program cel.Program
}

/*
This section of code is for custom library for semver comparison in CEL
The code is inspired by https://github.com/google/cel-go/blob/master/cel/cel_test.go#L46

The semver_compare is written based on `Compare` function in https://github.com/blang/semver
particularly in https://github.com/blang/semver/blob/master/semver.go#L125

Example:
`semver_compare(v1, v2)` is equivalent of `v1.Compare(v2)` in blang/semver library

The result is `semver_compare` is an integer just like `Compare`. So, the CEL
expression `semver_compare(v1, v2) == 0` is equivalent v1.Compare(v2) == 0. In
the other words, it checks if v1 is equal to v2 in term of semver comparision.
*/
type semverLib struct{}

func (semverLib) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Declarations(
			decls.NewFunction("semver_compare",
				decls.NewOverload("semver_compare",
					[]*exprpb.Type{decls.Any, decls.Any},
					decls.Int))),
	}
}

func (semverLib) ProgramOptions() []cel.ProgramOption {
	return []cel.ProgramOption{
		cel.Functions(
			&functions.Overload{
				Operator: "semver_compare",
				Binary:   semverCompare,
			},
		),
	}
}

func semverCompare(val1, val2 ref.Val) ref.Val {
	v1, err := semver.ParseTolerant(fmt.Sprint(val1.Value()))
	if err != nil {
		return types.ValOrErr(val1, "unable to parse '%v' to semver format", val1.Value())
	}

	v2, err := semver.ParseTolerant(fmt.Sprint(val2.Value()))
	if err != nil {
		return types.ValOrErr(val2, "unable to parse '%v' to semver format", val2.Value())
	}
	return types.Int(v1.Compare(v2))
}

// Evaluate to evaluate the compiled CEL program against input data (map)
func (e CelProgram) Evaluate(data map[string]interface{}) (bool, error) {
	result, _, err := e.program.Eval(data)
	if err != nil {
		return false, err
	}

	// we should have already ensured that this will be types.Bool during compilation
	if b, ok := result.Value().(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("cel expression evalutated to %T, not bool", result.Value())
}

// Validate to validate the CEL expression string by compiling it into CEL program
func (e *CelEnvironment) Validate(rule string) (CelProgram, error) {
	var celProg CelProgram
	ast, issues := e.env.Compile(rule)
	if err := issues.Err(); err != nil {
		return celProg, err
	}

	if ast.ResultType() != decls.Bool {
		return celProg, fmt.Errorf("cel expressions must have type Bool")
	}

	prog, err := e.env.Program(ast)
	if err != nil {
		return celProg, err
	}
	return CelProgram{program: prog}, nil
}
