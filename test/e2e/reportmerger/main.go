package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onsi/ginkgo/reporters"
)

func main() {
	flag.Parse()

	var merged reporters.JUnitTestSuite
	for _, input := range flag.Args() {
		func(input string) {
			fd, err := os.Open(input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to open file %q: %s\n", input, err)
				os.Exit(1)
			}
			defer func() {
				if err := fd.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "warning: error while closing %q: %s\n", input, err)
				}
			}()

			var suite reporters.JUnitTestSuite
			if err := xml.NewDecoder(fd).Decode(&suite); err != nil {
				fmt.Fprintf(os.Stderr, "failed to decode xml from %q: %s\n", input, err)
				os.Exit(1)
			}

			if merged.Name == "" {
				merged.Name = suite.Name
			}
			merged.Tests += suite.Tests
			merged.Failures += suite.Failures
			merged.Errors += suite.Errors
			merged.Time += suite.Time
			merged.TestCases = append(merged.TestCases, suite.TestCases...)
		}(input)
	}

	if _, err := io.Copy(os.Stdout, strings.NewReader(xml.Header)); err != nil {
		fmt.Fprintf(os.Stderr, "error writing output: %s\n", err)
		os.Exit(1)
	}

	e := xml.NewEncoder(os.Stdout)
	e.Indent("", "  ")
	if err := e.Encode(&merged); err != nil {
		fmt.Fprintf(os.Stderr, "error writing output: %s\n", err)
		os.Exit(1)
	}

	if _, err := os.Stdout.WriteString("\n"); err != nil {
		fmt.Fprintf(os.Stderr, "error writing output: %s\n", err)
		os.Exit(1)
	}

	if merged.Failures > 0 || merged.Errors > 0 {
		os.Exit(2)
	}
}
