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

type Cel struct {
	Rule string
}

type Evaluator interface {
	Evaluate(env map[string]interface{}) (bool, error)
}

type EvaluatorProvider interface {
	Evaluator(rule string) (Evaluator, error)
}

func NewCelEvaluatorProvider() *celEvaluatorProvider {
	env, err := cel.NewEnv(cel.Declarations(
		decls.NewVar("properties", decls.NewListType(decls.NewMapType(decls.String, decls.Any)))),
		cel.Lib(semverLib{}),
	)
	if err != nil {
		panic(err)
	}
	return &celEvaluatorProvider{
		env: env,
	}
}

type celEvaluatorProvider struct {
	env *cel.Env
}

type celEvaluator struct {
	p cel.Program
}

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

func (e celEvaluator) Evaluate(env map[string]interface{}) (bool, error) {
	result, _, err := e.p.Eval(env)
	if err != nil {
		return false, err
	}

	// we should have already ensured that this will be types.Bool during compilation
	if b, ok := result.Value().(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("cel expression evalutated to %T, not bool", result.Value())
}

func (e *celEvaluatorProvider) Evaluator(rule string) (Evaluator, error) {
	ast, issues := e.env.Compile(rule)
	if err := issues.Err(); err != nil {
		return nil, err
	}

	if ast.ResultType() != decls.Bool {
		return nil, fmt.Errorf("cel expressions must have type Bool")
	}

	p, err := e.env.Program(ast)
	if err != nil {
		return nil, err
	}
	return celEvaluator{p: p}, nil
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
