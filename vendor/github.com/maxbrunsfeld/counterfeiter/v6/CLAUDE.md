# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`counterfeiter` is a CLI (module `github.com/maxbrunsfeld/counterfeiter/v6`) that generates Go test doubles ("fakes") for interfaces, function types, and whole packages. It is typically invoked via `//go:generate` directives. Requires Go modules; CI runs on `stable` and `oldstable` Go on Linux and Windows.

## Commands

Full CI pipeline (vet → regenerate fakes → verify clean git tree → tests):

```shell
./scripts/ci.sh          # Linux/macOS
.\scripts\ci.ps1         # Windows
```

Individual steps:

```shell
go vet ./...
go generate ./...                  # regenerate all fakes under fixtures/ (directives use `go run`, so no install needed)
./scripts/checkclean.sh            # fail if regenerated fakes differ from committed ones
./scripts/cleanfakes.sh            # delete every */*fakes/fake*.go (then `go generate ./...` to rebuild)
go test -race . ./fixtures/...   # packages that exercise fakes or the generator concurrently
go test ./arguments/ ./command/ ./generator/ ./integration/
```

Run a single package's tests or a single spec. Tests use `sclevine/spec` + `gomega`; spec names are nested, so match with a regex on the top-level test function and the spec path:

```shell
go test ./generator/ -run TestGenerator
go test ./integration/ -run 'TestIntegration/round_trip_as_module/working_with_a_module'
go test ./arguments/ -run TestParsingArguments -v
go test ./command/ -run TestRunner
go test -run TestFakes .                           # generated_fakes_test.go at repo root
go test -race -run TestConcurrency .              # concurrency_test.go at repo root; only meaningful with -race
go test -bench . -benchmem .                       # benchmark_test.go at repo root
```

Debug env vars: `COUNTERFEITER_DEBUG=1` enables log output; `COUNTERFEITER_DISABLECACHE=1` bypasses the package-load cache; `COUNTERFEITER_PROFILE=1` writes `counterfeiter.profile`; `COUNTERFEITER_NO_GENERATE_WARNING=1` silences the "use -generate" warning. In tests, `log.SetOutput(io.Discard)` is set in the top-level test functions — comment it out to see generator logs.

## Architecture

Pipeline for one run (`main.go` is intentionally thin and should stay that way):

1. **`command.Detect`** (`command/runner.go`) turns the process into a list of `Invocation`s. In normal mode that is the single CLI invocation (it reads `GOFILE`/`GOLINE` from `go generate`). In `-generate` mode it scans every `.go` file in the cwd package for lines starting with `//counterfeiter:generate ` and builds one invocation per line. This is why `-generate` is much faster than many `//go:generate` lines: one process, one package load.
2. **`arguments.New`** (`arguments/parser.go`) parses each invocation's flags and positional args into `ParsedArguments`: source package dir, package path, interface name, fake name (`Fake` + exported interface name), output path (default `<pkgdir>/<pkg>fakes/fake_<snake_case>.go`), destination package name, and modes (`-p` package mode, `-` print to stdout, `-q`, `-header`). A `-header` on the top-level `-generate` line is inherited by directives that lack one (handled in `main.go`).
3. **`generator.NewFake`** (`generator/fake.go`) loads packages with `golang.org/x/tools/go/packages` (`loader.go`), finds the target `types.TypeName` (`findPackage`), and populates the `Fake` struct: `Methods` (from `interface_loader.go` / `package_loader.go`) or a single `Function` (`function_loader.go`), `Params`/`Returns` (`param.go`, `return.go`), and `Imports`.
4. **`Fake.Generate`** executes one of three `text/template`s — `interface_template.go`, `function_template.go`, `package_template.go` — then runs `goimports` (`imports.Process`) on the output. `main.go` runs `go/format` again and writes the file.

Key supporting pieces:

- **`generator.Imports`** (`import.go`) dedupes imports by package path and guarantees unique aliases (appends `a`, `b`, … on collision). `addImportsFor` in `loader.go` walks `types.Type` recursively to collect every package a fake needs; add a case there when a new `types.Type` kind shows up (it logs `!!! WARNING: Missing case`).
- **Generics**: `findPackage` / `getGenericTypeData` (`loader.go`) extract type params/constraints into `GenericTypeParameters*` strings used by the template. The compile-time assertion for a generic fake is emitted inside a blank generic func (`func _[T C]() { var _ pkg.I[T] = new(FakeI[T]) }`) so any constraint kind works. A target that is itself a constraint interface (unions or `~T`) is rejected up front because it cannot be implemented.
- **Package loading** (`loadPackages` in `loader.go`): the target package is the root of a single `packages.Load` call with `NeedName | NeedFiles | NeedImports | NeedTypes | NeedSyntax`, deliberately without `NeedDeps` or `NeedTypesInfo`. go/packages then type-checks only the target package from source and reads everything it imports from export data via `go list -export` (built through the normal go build cache), instead of type-checking the whole dependency graph on every run. `NeedSyntax` is what forces the root itself to come from source: export data omits unexported types that nothing exported refers to, which would break faking an unexported interface. When the target does not compile, `go list -export` attaches the compiler transcript as an unpositioned `ListError`, which `loadPackages` skips (`isBuildTranscript`) in favor of the positioned type errors. Nothing in the generator reads `TypesInfo`; do not add it back. Of the target package's own errors only `ParseError` is fatal; the rest (unresolved imports, type errors in other files) are kept in `Fake.loadErrors` and generation proceeds. It fails later, quoting them, only if the target's signatures mention an unresolved type (`hasInvalidType`, which does not look inside named types because they print by name) or the interface embeds one (`hasInvalidEmbed`, since the type checker silently drops such an embed from the method set).
- **Same-package fakes**: `main.go` passes the output directory via `generator.WithDestinationDir`. When it equals the target package's directory (`packages.Package.Dir`, symlinks resolved), `findPackage` sets `inTargetPackage`: the target package is not imported, its names print unqualified (the `types.Qualifier` returns `""` for packages absent from `Imports`), and the assertion is emitted even for unexported targets. A stale same-package fake never blocks regeneration because its type errors are tolerated like any other (see package loading). Extend `NewFake` only through `...Option`; its existing parameters are public API.
- **`Cacher`** (`cache.go`): `Cache` memoizes `packages.Load` results per package path across invocations in a `-generate` run; `FakeCache` is the no-op used by tests and `COUNTERFEITER_DISABLECACHE`.
- **`ctx.go`** holds `getBuildContext`, which returns `build.Default` with `build.Context.Dir` set to the working directory.

## Testing conventions

- `fixtures/` is a large corpus of interfaces exercising edge cases (aliases, dot imports, variadics, embedded interfaces, generics, hyphenated packages, package mode, vendored-style external packages, etc.). Each has `//go:generate` or `//counterfeiter:generate` directives; the generated fakes live in sibling `*fakes/` dirs and **are committed**. CI fails if `go generate ./...` produces a diff, so after changing templates or the generator, run `go generate ./...` and commit the regenerated fakes.
- `generated_fakes_test.go` (repo root) uses the committed fixture fakes as a behavioral test of the fake API (`Stub`, `CallCount`, `ArgsForCall`, `Returns`, `ReturnsOnCall`, `Invocations`).
- `concurrency_test.go` (repo root) drives `generator.NewFake` / `Generate` and `CachedFileReader` from several goroutines through shared caches. It only proves anything under `-race`, which is why it lives in the root package: CI runs `-race` there and on `fixtures/...` only, since `generator`, `integration`, `arguments`, and `command` are single-threaded and the race detector triples their run time. Anything concurrent added to the tool needs coverage here, not in its own package.
- `generator/generator_internals_test.go` unit-tests `NewFake`/`Generate` against fixtures directly.
- `integration/roundtrip_test.go` copies fixtures into a temp module, generates fakes, and runs `go build` on the result; some cases compare byte-for-byte against `integration/testdata/expected_*.txt`. Set `writeToTestData = true` in that file to dump actual output to `integration/testdata/output/` (gitignored) when debugging a mismatch.
- `.golangci.yaml` skips `fixtures/` from linting.

## Package layout

Keep the existing layout: `main.go` at the module root and the three subpackages `arguments`, `command`, and `generator`. `main.go` must stay at the root because users invoke the tool by module path (`go run github.com/maxbrunsfeld/counterfeiter/v6`, `go get -tool ...`); do not move it under `cmd/`. Do not restructure packages toward a different layout (for example Ben Johnson's Standard Package Layout, which the maintainer likes in general but has chosen not to apply here). Put new code in whichever of the three existing packages owns that pipeline stage.

## Development workflow: red → green → refactor

Every fix or feature follows strict TDD:

1. **Red** — write a spec (`sclevine/spec` + `gomega`, matching the surrounding test file) that reproduces the issue and fails. For generator behavior this usually means adding a fixture interface under `fixtures/` plus an assertion in `generator/generator_internals_test.go` or `integration/roundtrip_test.go`. Run it and confirm it fails for the expected reason.
2. **Green** — write the simplest code that makes the spec pass. Nothing more.
3. **Refactor** — with the spec passing, look for duplication or simplification opportunities in the code just touched, keeping the suite green. Then regenerate fakes (`go generate ./...`) and run `./scripts/ci.sh`.

Do not skip step 1, even for "obvious" one-line fixes.

## Commits and pull requests

Every commit carries a DCO sign-off from the person making it: always `git commit -s`.

Write commit messages and PR descriptions in plain, first-person prose, the way the maintainer does (see `git log --author=Fitzgerald` and PRs #123, #124, #125):

- Subject: short, lowercase, imperative ("allow fakes to be generated into the interface's own package"). No conventional-commit prefixes.
- Body only when the diff does not explain itself: what was wrong, what changes for the user. A few sentences, not headings or bullet inventories of files and functions.
- `Fixes #n` on its own line at the end when it closes an issue.
- PR body: describe the problem and the outcome for users, not the commit list. No commit hashes, no narrating the process (do not mention TDD, red/green, or how carefully it was tested; just say what is covered). Mention issues that are fixed or related at the end.
- Squash follow-up tweaks into the commit they belong to before pushing.
- No AI attribution or co-author lines.

## Contribution constraints (from README)

Keep `main.go` simple, avoid adding CLI options, avoid adding internal complexity, and keep unit coverage high.
