# astgen-go [![CI Status](https://github.com/itchyny/astgen-go/workflows/CI/badge.svg)](https://github.com/itchyny/astgen-go/actions)
Build Go code from arbitrary value in Go.

## Usage
```go
package main

import (
	"go/printer"
	"go/token"
	"log"
	"os"

	"github.com/itchyny/astgen-go"
)

type X struct {
	x int
	y Y
	z *Z
}

type Y struct {
	y int
}

type Z struct {
	s string
	t map[string]int
}

func main() {
	x := &X{1, Y{2}, &Z{"hello", map[string]int{"x": 42}}}
	t, err := astgen.Build(x)
	if err != nil {
		log.Fatal(err)
	}
	err = printer.Fprint(os.Stdout, token.NewFileSet(), t)
	if err != nil {
		log.Fatal(err)
	}
}
```
```go
&X{x: 1, y: Y{y: 2}, z: &Z{s: "hello", t: map[string]int{"x": 42}}}
```

## Bug Tracker
Report bug at [Issues・itchyny/astgen-go - GitHub](https://github.com/itchyny/astgen-go/issues).

## Author
itchyny (https://github.com/itchyny)

## License
This software is released under the MIT License, see LICENSE.
