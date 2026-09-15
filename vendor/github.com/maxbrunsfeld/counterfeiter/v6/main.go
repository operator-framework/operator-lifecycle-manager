package main

import (
	"errors"
	"fmt"
	"go/format"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/pprof"

	"github.com/maxbrunsfeld/counterfeiter/v6/arguments"
	"github.com/maxbrunsfeld/counterfeiter/v6/command"
	"github.com/maxbrunsfeld/counterfeiter/v6/generator"
)

func main() {
	debug.SetGCPercent(-1)

	if err := run(); err != nil {
		fail("%v", err)
	}
}

func run() error {
	profile := os.Getenv("COUNTERFEITER_PROFILE") != ""
	if profile {
		p, err := filepath.Abs(filepath.Join(".", "counterfeiter.profile"))
		if err != nil {
			return err
		}
		f, err := os.Create(p)
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			return err
		}
		fmt.Printf("Profile: %s\n", p)
		defer pprof.StopCPUProfile()
	}

	log.SetFlags(log.Lshortfile)
	if !isDebug() {
		log.SetOutput(io.Discard)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return errors.New("Error - couldn't determine current working directory")
	}

	var cache generator.Cacher
	var headerReader generator.FileReader
	if disableCache() {
		cache = &generator.FakeCache{}
		headerReader = &generator.SimpleFileReader{}
	} else {
		cache = &generator.Cache{}
		headerReader = &generator.CachedFileReader{}
	}
	var invocations []command.Invocation
	var args *arguments.ParsedArguments
	args, _ = arguments.New(os.Args, cwd, filepath.EvalSymlinks, os.Stat)
	generateMode := false
	if args != nil {
		generateMode = args.GenerateMode
	}
	if !generateMode && shouldPrintGenerateWarning() {
		fmt.Printf("\nWARNING: Invoking counterfeiter multiple times from \"go generate\" is slow.\nConsider using counterfeiter:generate directives to speed things up.\nSee https://github.com/maxbrunsfeld/counterfeiter#step-2b---add-counterfeitergenerate-directives for more information.\nSet the \"COUNTERFEITER_NO_GENERATE_WARNING\" environment variable to suppress this message.\n\n")
	}
	invocations, err = command.Detect(cwd, os.Args, generateMode)
	if err != nil {
		return err
	}

	for i := range invocations {
		// Flags on the '//go:generate ... -generate' line are the defaults for
		// every '//counterfeiter:generate' directive in the package.
		a, err := arguments.New(invocations[i].Args, cwd, filepath.EvalSymlinks, os.Stat, arguments.WithDefaults(args))
		if err != nil {
			return err
		}

		err = generate(cwd, a, cache, headerReader)
		if err != nil {
			return err
		}
	}
	return nil
}

func isDebug() bool {
	return os.Getenv("COUNTERFEITER_DEBUG") != ""
}

func disableCache() bool {
	return os.Getenv("COUNTERFEITER_DISABLECACHE") != ""
}

func shouldPrintGenerateWarning() bool {
	return invokedByGoGenerate() && os.Getenv("COUNTERFEITER_NO_GENERATE_WARNING") == ""
}

func invokedByGoGenerate() bool {
	return os.Getenv("DOLLAR") == "$"
}

func generate(workingDir string, args *arguments.ParsedArguments, cache generator.Cacher, headerReader generator.FileReader) error {
	f, err := newFake(workingDir, args, cache, headerReader)
	if err != nil {
		return err
	}

	if !args.Quiet {
		if err := reportStarting(workingDir, args.OutputPath, f.Name); err != nil {
			return err
		}
	}

	b, err := f.Generate(true)
	if err != nil {
		return err
	}

	if err := printCode(b, args.OutputPath, args.PrintToStdOut); err != nil {
		return err
	}

	if !args.Quiet {
		fmt.Fprint(os.Stderr, "Done\n")
	}

	return nil
}

func newFake(workingDir string, args *arguments.ParsedArguments, cache generator.Cacher, headerReader generator.FileReader) (*generator.Fake, error) {
	mode := generator.InterfaceOrFunction
	if args.GenerateInterfaceAndShimFromPackageDirectory {
		mode = generator.Package
	}

	headerContent, err := headerReader.Get(workingDir, args.HeaderFile)
	if err != nil {
		return nil, err
	}

	opts := []generator.Option{generator.WithDestinationDir(filepath.Dir(args.OutputPath))}
	if args.TestPackage {
		opts = append(opts, generator.WithTestPackage())
	}
	if args.FakeNameExplicit {
		opts = append(opts, generator.WithExplicitName())
	}
	return generator.NewFake(mode, args.InterfaceName, args.PackagePath, args.FakeImplName, args.DestinationPackageName, headerContent, workingDir, cache, opts...)
}

func printCode(code []byte, outputPath string, printToStdOut bool) error {
	formattedCode, err := format.Source(code)
	if err != nil {
		return err
	}

	if printToStdOut {
		fmt.Println(string(formattedCode))
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(outputPath), 0777)
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("Couldn't create fake file - %v", err)
	}

	_, err = file.Write(formattedCode)
	if err != nil {
		return fmt.Errorf("Couldn't write to fake file - %v", err)
	}
	return nil
}

func reportStarting(workingDir string, outputPath, fakeName string) error {
	rel, err := filepath.Rel(workingDir, outputPath)
	if err != nil {
		return err
	}

	msg := fmt.Sprintf("Writing `%s` to `%s`... ", fakeName, rel)
	if isDebug() {
		msg = msg + "\n"
	}
	fmt.Fprint(os.Stderr, msg)
	return nil
}

func fail(s string, args ...interface{}) {
	fmt.Printf("\n"+s+"\n", args...)
	os.Exit(1)
}
