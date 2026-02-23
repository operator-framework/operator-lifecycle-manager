package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	ginkgoDescribeFunctionName = "Describe"
	ginkgoLabelFunctionName    = "Label"
)

var logger = logrus.New()

type options struct {
	numChunks  int
	printChunk int
	printDebug bool
	writer     io.Writer
	logLevel   string
}

func main() {
	opts := options{
		writer: os.Stdout,
	}
	flag.IntVar(&opts.numChunks, "chunks", 1, "Number of chunks to create focus regexps for")
	flag.IntVar(&opts.printChunk, "print-chunk", 0, "Chunk to print a regexp for")
	flag.BoolVar(&opts.printDebug, "print-debug", false, "Print all spec prefixes in non-regexp format. Use for debugging")
	flag.StringVar(&opts.logLevel, "log-level", logrus.ErrorLevel.String(), "Configure the logging level")
	flag.Parse()

	if opts.printChunk >= opts.numChunks {
		log.Fatal(fmt.Errorf("the chunk to print (%d) must be a smaller number than the number of chunks (%d)", opts.printChunk, opts.numChunks))
	}

	dir := flag.Arg(0)
	if dir == "" {
		log.Fatal(fmt.Errorf("test directory required as the argument"))
	}

	var err error
	level, err := logrus.ParseLevel(opts.logLevel)
	if err != nil {
		log.Fatal(err)
	}
	logger.SetLevel(level)

	dir, err = getPathRelativeToCwd(dir)
	if err != nil {
		log.Fatal(err)
	}

	if err := opts.run(dir); err != nil {
		log.Fatal(err)
	}
}

func getPathRelativeToCwd(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Rel(wd, path)
}

func (opts options) run(dir string) error {
	// Get all test labels
	labels, err := findLabels(dir)
	if err != nil {
		return err
	}
	sort.Strings(labels)

	var out string
	if opts.printDebug {
		out = strings.Join(labels, "\n")
	} else {
		out, err = createFilterLabelChunk(opts.numChunks, opts.printChunk, labels)
		if err != nil {
			return err
		}
	}

	fmt.Fprint(opts.writer, out)
	return nil
}

func findLabels(dir string) ([]string, error) {
	var labels []string
	logger.Infof("Finding labels for ginkgo tests in path: %s", dir)
	matches, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	for _, match := range matches {
		labels = append(labels, extractLabelsFromFile(match)...)
	}
	return labels, nil
}

func extractLabelsFromFile(filename string) []string {
	var labels []string

	// Create a Go source file set
	fs := token.NewFileSet()
	node, err := parser.ParseFile(fs, filename, nil, parser.AllErrors)
	if err != nil {
		fmt.Printf("Error parsing file %s: %v\n", filename, err)
		return labels
	}

	ast.Inspect(node, func(n ast.Node) bool {
		if callExpr, ok := n.(*ast.CallExpr); ok {
			if fun, ok := callExpr.Fun.(*ast.Ident); ok && fun.Name == ginkgoDescribeFunctionName {
				for _, arg := range callExpr.Args {
					if ce, ok := arg.(*ast.CallExpr); ok {
						if labelFunc, ok := ce.Fun.(*ast.Ident); ok && labelFunc.Name == ginkgoLabelFunctionName {
							for _, arg := range ce.Args {
								if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
									labels = append(labels, strings.Trim(lit.Value, "\""))
								}
							}
						}
					}
				}
			}
		}
		return true
	})

	return labels
}

func createFilterLabelChunk(numChunks, printChunk int, specs []string) (string, error) {
	numSpecs := len(specs)
	if numSpecs < numChunks {
		return "", fmt.Errorf("have more desired chunks (%d) than specs (%d)", numChunks, numSpecs)
	}

	// Create chunks of size ceil(number of specs/number of chunks) in alphanumeric order.
	// This is deterministic on inputs.
	chunks := make([][]string, numChunks)
	interval := int(math.Ceil(float64(numSpecs) / float64(numChunks)))
	currIdx := 0
	for chunkIdx := 0; chunkIdx < numChunks; chunkIdx++ {
		nextIdx := int(math.Min(float64(currIdx+interval), float64(numSpecs)))
		chunks[chunkIdx] = specs[currIdx:nextIdx]
		currIdx = nextIdx
	}

	chunk := chunks[printChunk]
	if len(chunk) == 0 {
		// This is a panic because the caller may skip this error, resulting in missed test specs.
		panic(fmt.Sprintf("bug: chunk %d has no elements", printChunk))
	}

	// Write out the regexp to focus chunk specs via `ginkgo -focus <re>`.
	var reStr string
	if len(chunk) == 1 {
		reStr = fmt.Sprintf("%s", chunk[0])
	} else {
		sb := strings.Builder{}
		sb.WriteString(chunk[0])
		for _, test := range chunk[1:] {
			sb.WriteString(" || ")
			sb.WriteString(test)
		}
		reStr = fmt.Sprintf("%s", sb.String())
	}

	return reStr, nil
}
