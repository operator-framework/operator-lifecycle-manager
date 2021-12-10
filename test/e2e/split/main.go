package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var topDescribeRE = regexp.MustCompile(`var _ = Describe\("(.+)", func\(.*`)

func main() {
	var numChunks, printChunk int
	flag.IntVar(&numChunks, "chunks", 1, "Number of chunks to create focus regexps for")
	flag.IntVar(&printChunk, "print-chunk", 0, "Chunk to print a regexp for")
	flag.Parse()

	if printChunk >= numChunks {
		log.Fatalf("the chunk to print (%d) must be a smaller number than the number of chunks (%d)", printChunk, numChunks)
	}

	dir := flag.Arg(0)

	// Clean dir.
	var err error
	if dir, err = filepath.Abs(dir); err != nil {
		log.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	if dir, err = filepath.Rel(wd, dir); err != nil {
		log.Fatal(err)
	}

	// Find all Ginkgo specs in dir's test files.
	// These can be grouped independently.
	describeTable := make(map[string]struct{})
	matches, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		log.Fatal(err)
	}
	for _, match := range matches {
		b, err := ioutil.ReadFile(match)
		if err != nil {
			log.Fatal(err)
		}
		specNames := topDescribeRE.FindAllSubmatch(b, -1)
		if len(specNames) == 0 {
			log.Printf("%s: found no top level describes, skipping", match)
			continue
		}
		for _, possibleNames := range specNames {
			if len(possibleNames) != 2 {
				log.Printf("%s: expected to find 2 submatch, found %d:", match, len(possibleNames))
				for _, name := range possibleNames {
					log.Printf("\t%s\n", string(name))
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
	sort.Strings(describes)

	chunks := make([][]string, numChunks)
	interval := int(math.Ceil(float64(len(describes)) / float64(numChunks)))
	currIdx := 0
	for chunkIdx := 0; chunkIdx < numChunks; chunkIdx++ {
		nextIdx := int(math.Min(float64(currIdx+interval), float64(len(describes))))
		chunks[chunkIdx] = describes[currIdx:nextIdx]
		currIdx = nextIdx
	}

	sb := strings.Builder{}
	sb.WriteString("(")
	sb.WriteString(chunks[printChunk][0])
	for _, test := range chunks[printChunk][1:] {
		sb.WriteString("|")
		sb.WriteString(test)
	}
	sb.WriteString(").*")

	fmt.Println(sb.String())
}
