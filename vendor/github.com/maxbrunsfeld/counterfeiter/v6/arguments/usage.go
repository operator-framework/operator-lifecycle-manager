package arguments

const usage = `
USAGE
	counterfeiter
		[-generate] [-o <output-path>] [-p] [-fake-name <fake-name>]
		[-fake-name-template <template>] [-header <header-file>] [-q] [-test]
		[<source-path>] <interface> [-]

ARGUMENTS
	source-path
		Path to the file or directory containing the interface to fake.
		In package mode (-p), source-path is the import path of the package
		to generate an interface and shim for; a standard library package
		can be given by name (e.g. "os").

	interface
		If source-path is specified: name of the interface to fake.
		If no source-path is specified: fully qualified path of the
		interface to fake, <package-path>.<interface>.
		Not used in package mode (-p), where the interface is named after
		the package.

	example:
		# in directory "mypackage", writes "FakeStdInterface" to
		# ./mypackagefakes/fake_std_interface.go
		counterfeiter package/subpackage.StdInterface

	'-' argument
		Write code to standard out instead of to a file

OPTIONS
	-generate
		Identify all //counterfeiter:generate directives in .go files in the
		current working directory and generate fakes for them. You can pass
		arguments as usual.

		NOTE: This is not the same as //go:generate directives
		(used with the 'go generate' command), but it can be combined with
		go generate by adding the following to a .go file:

		# runs counterfeiter in generate mode
		//go:generate go tool counterfeiter -generate

	example:
		Add the following to a .go file:

		//counterfeiter:generate . MyInterface
		//counterfeiter:generate . MyOtherInterface
		//counterfeiter:generate . MyThirdInterface

		# run counterfeiter
		counterfeiter -generate
		# writes "FakeMyInterface" to ./mypackagefakes/fake_my_interface.go
		# writes "FakeMyOtherInterface" to ./mypackagefakes/fake_my_other_interface.go
		# writes "FakeMyThirdInterface" to ./mypackagefakes/fake_my_third_interface.go

		The -o, -fake-name-template, -header, -q and -test flags given
		alongside -generate are the defaults for every directive. A directive's
		own flags take precedence.

	example:
		# every fake goes into ./fake and is named after its interface
		//go:generate go tool counterfeiter -generate -o fake -fake-name-template {{.TargetName}}
		//counterfeiter:generate . MyInterface
		//counterfeiter:generate -o otherfake . MyOtherInterface

		# writes "MyInterface" to ./fake/my_interface.go
		# writes "MyOtherInterface" to ./otherfake/my_other_interface.go

	-o
		Path to the file or directory for the generated fakes.
		This also determines the package name that will be used:
		if the directory already holds a Go package the fake joins
		it, otherwise the package is named after the directory.
		By default, the generated fakes will be generated in
		the package "xyzfakes" which is nested in package "xyz",
		where "xyz" is the name of referenced package.

	example:
		# writes "FakeMyInterface" to ./mySpecialFakesDir/specialFake.go
		counterfeiter -o ./mySpecialFakesDir/specialFake.go ./mypackage MyInterface

		# writes "FakeMyInterface" to ./mySpecialFakesDir/fake_my_interface.go
		counterfeiter -o ./mySpecialFakesDir ./mypackage MyInterface

	-test
		Generate the fake into the external test package ("<package>_test")
		of the output directory, in a _test.go file, so it is only compiled
		for tests and tests in that package can use it unqualified. Without -o the
		fake is written into the current directory, next to the tests that
		use it, wherever the interface comes from. The interface's package
		is imported as usual, so the interface must be exported.
		Cannot be combined with -p.

	example:
		# writes "FakeMyInterface" to ./fake_my_interface_test.go, in package "mypackage_test"
		counterfeiter -test . MyInterface

		# writes "FakeOtherInterface" to ./fake_other_interface_test.go, in package "mypackage_test"
		counterfeiter -test ../otherpackage OtherInterface

		# writes "FakeWriteCloser" to ./fake_write_closer_test.go, in package "mypackage_test"
		counterfeiter -test io.WriteCloser

	-p
		Package mode: counterfeiter generates an interface and a shim
		implementation for a package in your module or the standard
		library. The interface has the package's exported functions as
		methods, and the shim forwards each method to the package. The
		generated file carries a //counterfeiter:generate directive, so
		running go generate there produces a fake of the interface.

	example:
		# writes the "Os" interface and "OsShim" to ${PWD}/osshim/os.go
		counterfeiter -p os
		# now generate "FakeOs" in ${PWD}/osshim/osshimfakes/fake_os.go
		go generate ./osshim/...

	-header
		Path to the file which should be used as a header for all generated fakes.
		By default, no special header is used.
		This is useful to e.g. add a licence header to every fake.

		In generate mode the header can be set once for the whole package on
		the "go:generate" line; a "counterfeiter:generate" line that specifies
		its own header file takes precedence.

	example:
		# having the following code in a package ...
		//go:generate go tool counterfeiter -header ./generic.go.txt -generate
		//counterfeiter:generate -header ./specific.go.txt . MyInterface
		//counterfeiter:generate . MyOtherInterface
		//counterfeiter:generate . MyThirdInterface

		# ... generating the fakes ...
		go generate .

		# writes "FakeMyInterface" with ./specific.go.txt as a header
		# writes "FakeMyOtherInterface" & "FakeMyThirdInterface" with ./generic.go.txt as a header

	-fake-name
		Name of the fake struct to generate, used as given. By default,
		'Fake' will be prepended to the name of the original interface;
		a fake of an unexported interface generated into the interface's
		own package is unexported. (ignored in -p mode)

	example:
		# writes "CoolThing" to ./mypackagefakes/cool_thing.go
		counterfeiter -fake-name CoolThing ./mypackage MyInterface

	-fake-name-template
		A text/template for the name of the fake struct, used when -fake-name
		is not given. {{.TargetName}} is the name of the interface being faked,
		with its first letter upper-cased. In generate mode it can be set once
		for the whole package on the "go:generate" line. (ignored in -p mode)

	example:
		# writes "MyInterfaceDouble" to ./mypackagefakes/my_interface_double.go
		counterfeiter -fake-name-template '{{.TargetName}}Double' ./mypackage MyInterface

	-q
		Suppress the "Writing ..." status lines. Errors are still reported.
`
