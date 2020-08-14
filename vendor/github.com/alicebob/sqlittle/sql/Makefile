.PHONY: all test build

all: test build

parser.go: parser.go.y lexer.go
	goyacc -o parser.go parser.go.y

test: parser.go
	go test

build: parser.go
	go build

get:
	go get golang.org/x/tools/cmd/goyacc
