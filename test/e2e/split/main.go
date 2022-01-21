package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

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
		exitIfErr(fmt.Errorf("the chunk to print (%d) must be a smaller number than the number of chunks (%d)", opts.printChunk, opts.numChunks))
	}

	dir := flag.Arg(0)
	if dir == "" {
		exitIfErr(fmt.Errorf("test directory required as the argument"))
	}

	// Clean dir.
	var err error
	dir, err = filepath.Abs(dir)
	exitIfErr(err)
	wd, err := os.Getwd()
	exitIfErr(err)
	dir, err = filepath.Rel(wd, dir)
	exitIfErr(err)

	exitIfErr(opts.run(dir))
}

func exitIfErr(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func (opts options) run(dir string) error {
	level, err := logrus.ParseLevel(opts.logLevel)
	if err != nil {
		return fmt.Errorf("failed to parse the %s log level: %v", opts.logLevel, err)
	}
	logger := logrus.New()
	logger.SetLevel(level)

	describes, err := findDescribes(logger, dir)
	if err != nil {
		return err
	}

	// Find minimal prefixes for all spec strings so no spec runs are duplicated across chunks.
	prefixes := findMinimalWordPrefixes(describes)
	sort.Strings(prefixes)

	var out string
	if opts.printDebug {
		out = strings.Join(prefixes, "\n")
	} else {
		out, err = createChunkRegexp(opts.numChunks, opts.printChunk, prefixes)
		if err != nil {
			return err
		}
	}

	fmt.Fprint(opts.writer, out)
	return nil
}

// TODO: this is hacky because top-level tests may be defined elsewise.
// A better strategy would be to use the output of `ginkgo -noColor -dryRun`
// like https://github.com/operator-framework/operator-lifecycle-manager/pull/1476 does.
var topDescribeRE = regexp.MustCompile(`var _ = Describe\("(.+)", func\(.*`)

func findDescribes(logger logrus.FieldLogger, dir string) ([]string, error) {
	// Find all Ginkgo specs in dir's test files.
	// These can be grouped independently.
	describeTable := make(map[string]struct{})
	matches, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	for _, match := range matches {
		b, err := ioutil.ReadFile(match)
		if err != nil {
			return nil, err
		}
		specNames := topDescribeRE.FindAllSubmatch(b, -1)
		if len(specNames) == 0 {
			logger.Warnf("%s: found no top level describes, skipping", match)
			continue
		}
		for _, possibleNames := range specNames {
			if len(possibleNames) != 2 {
				logger.Debugf("%s: expected to find 2 submatch, found %d:", match, len(possibleNames))
				for _, name := range possibleNames {
					logger.Debugf("\t%s\n", string(name))
				}
				continue
			}
			describe := strings.TrimSpace(string(possibleNames[1]))
			describeTable[describe] = struct{}{}
		}
	}

	describes := make([]string, len(describeTable))
	i := 0
	for describeKey := range describeTable {
		describes[i] = describeKey
		i++
	}
	return describes, nil
}

func createChunkRegexp(numChunks, printChunk int, specs []string) (string, error) {
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
		reStr = fmt.Sprintf("%s .*", chunk[0])
	} else {
		sb := strings.Builder{}
		sb.WriteString(chunk[0])
		for _, test := range chunk[1:] {
			sb.WriteString("|")
			sb.WriteString(test)
		}
		reStr = fmt.Sprintf("(%s) .*", sb.String())
	}

	return reStr, nil
}

func findMinimalWordPrefixes(specs []string) (prefixes []string) {
	// Create a word trie of all spec strings.
	t := make(wordTrie)
	for _, spec := range specs {
		t.push(spec)
	}

	// Now find the first branch point for each path in the trie by DFS.
	for word, node := range t {
		var prefixElements []string
	next:
		if word != "" {
			prefixElements = append(prefixElements, word)
		}
		if len(node.children) == 1 {
			for nextWord, nextNode := range node.children {
				word, node = nextWord, nextNode
			}
			goto next
		}
		// TODO: this might need to be joined by "\s+"
		// in case multiple spaces were used in the spec name.
		prefixes = append(prefixes, strings.Join(prefixElements, " "))
	}

	return prefixes
}

// wordTrie is a trie of word nodes, instead of individual characters.
type wordTrie map[string]*wordTrieNode

type wordTrieNode struct {
	word     string
	children map[string]*wordTrieNode
}

// push creates s branch of the trie from each word in s.
func (t wordTrie) push(s string) {
	split := strings.Split(s, " ")

	curr := &wordTrieNode{word: "", children: t}
	for _, sp := range split {
		if sp = strings.TrimSpace(sp); sp == "" {
			continue
		}
		next, hasNext := curr.children[sp]
		if !hasNext {
			next = &wordTrieNode{word: sp, children: make(map[string]*wordTrieNode)}
			curr.children[sp] = next
		}
		curr = next
	}
	// Add termination node so "foo" and "foo bar" have a branching point of "foo".
	curr.children[""] = &wordTrieNode{}
}
