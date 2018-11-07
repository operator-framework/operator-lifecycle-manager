# `counterfeiter` [![Build Status](https://travis-ci.org/maxbrunsfeld/counterfeiter.svg?branch=master)](https://travis-ci.org/maxbrunsfeld/counterfeiter) [![Build status](https://ci.appveyor.com/api/projects/status/0j2v7pt06lp9yanm/branch/master?svg=true)](https://ci.appveyor.com/project/maxbrunsfeld/counterfeiter/branch/master)

When writing unit-tests for an object, it is often useful to have fake implementations
of the object's collaborators. In go, such fake implementations cannot be generated
automatically at runtime, and writing them by hand can be quite arduous.

`counterfeiter` allows you to simply generate test doubles for a given interface.

### Install

 ```shell
go get -u github.com/maxbrunsfeld/counterfeiter
```

### Generating Test Doubles

Given a path to a package and an interface name, you can generate a test double.

```shell
$ cat path/to/foo/file.go
```

```go
package foo

type MySpecialInterface interface {
    DoThings(string, uint64) (int, error)
}
```

```shell
$ counterfeiter path/to/foo MySpecialInterface
Wrote `FakeMySpecialInterface` to `path/to/foo/foofakes/fake_my_special_interface.go`
```

### Using Test Doubles In Your Tests

Instantiate fakes`:

```go
import "my-repo/path/to/foo/foofakes"

var fake = &foofakes.FakeMySpecialInterface{}
```

Fakes record the arguments they were called with:

```go
fake.DoThings("stuff", 5)

Expect(fake.DoThingsCallCount()).To(Equal(1))

str, num := fake.DoThingsArgsForCall(0)
Expect(str).To(Equal("stuff"))
Expect(num).To(Equal(uint64(5)))
```

You can stub their return values:

```go
fake.DoThingsReturns(3, errors.New("the-error"))

num, err := fake.DoThings("stuff", 5)
Expect(num).To(Equal(3))
Expect(err).To(Equal(errors.New("the-error")))
```

For more examples of using the `counterfeiter` API, look at [some of the provided examples](https://github.com/maxbrunsfeld/counterfeiter/blob/master/counterfeiter_test.go).

### Using `go generate`

It can be frustrating when you change your interface declaration and suddenly all of your generated code is suddenly out-of-date. The best practice here is to use golang's ["go generate" command](https://blog.golang.org/generate) to make it easier to keep your test doubles up to date.

```shell
$ cat path/to/foo/file.go
```

```go
package foo

//go:generate counterfeiter . MySpecialInterface
type MySpecialInterface interface {
    DoThings(string, uint64) (int, error)
}
```

```shell
$ go generate ./...
Wrote `FakeMySpecialInterface` to `path/to/foo/foofakes/fake_my_special_interface.go`
```

### Running The Tests For `counterfeiter`

If you want to run the tests for `counterfeiter` (perhaps, because you want to contribute a PR), all you have to do is run `scripts/ci.sh`.

### Contributions

So you want to contribute to `counterfeiter`! That's great, here's exactly what you should do:

* open a new github issue, describing your problem, or use case
* help us understand how you want to fix or extend `counterfeiter`
* write one or more unit tests for the behavior you want
* write the simplest code you can for the feature you're working on
* try to find any opportunities to refactor
* avoid writing code that isn't covered by unit tests

`counterfeiter` has a few high level goals for contributors to keep in mind

* keep unit-level test coverage as high as possible
* keep `main.go` as simple as possible
* avoid making the command line options any more complicated
* avoid making the internals of `counterfeiter` any more complicated

If you have any questions about how to contribute, rest assured that @tjarratt and other maintainers will work with you to ensure we make `counterfeiter` better, together. This project has largely been maintained by the community, and we greatly appreciate any PR (whether big or small).

### License

`counterfeiter` is MIT-licensed.
