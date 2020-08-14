.PHONY: all test ci bench format fuzz readme

all: format test

test:
	go test ./...

ci:
	go test -tags ci ./...

bench:
	go test -bench . github.com/alicebob/sqlittle/db

format:
	go fmt

fuzz:
	go get -v github.com/dvyukov/go-fuzz/...

	rm -f sqlittle-fuzz.zip
	go-fuzz-build github.com/alicebob/sqlittle/db
	mkdir -p workdir
	cp -r corpus workdir
	go-fuzz -bin=sqlittle-fuzz.zip -workdir=workdir

readme:
	go get github.com/jimmyfrasche/autoreadme
	autoreadme -f -template README.template
