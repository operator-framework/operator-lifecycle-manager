package main

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(t *testing.T) {
	// This test makes sure that every spec gets run.

	cmd := exec.Command("./test/e2e/split/integration_test.sh")
	cmd.Dir = "../../../"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	require.NoError(t, err)
}

func TestCreateChunkRegexp(t *testing.T) {
	type spec struct {
		name       string
		numChunks  int
		printChunk int
		specs      []string
		expRE      string
		expError   string
	}

	cases := []spec{
		{
			name:      "singlePrefix1",
			numChunks: 1, printChunk: 0,
			specs: []string{"foo"},
			expRE: "foo .*",
		},
		{
			name:      "multiplePrefixes1",
			numChunks: 1, printChunk: 0,
			specs: []string{"bar foo", "baz", "foo"},
			expRE: "(bar foo|baz|foo) .*",
		},
		{
			name:      "multiplePrefixes2",
			numChunks: 3, printChunk: 0,
			specs: []string{"bar foo", "baz", "foo"},
			expRE: "bar foo .*",
		},
		{
			name:      "multiplePrefixes3",
			numChunks: 3, printChunk: 2,
			specs: []string{"bar foo", "baz", "foo"},
			expRE: "foo .*",
		},
		{
			name:      "empty",
			numChunks: 1, printChunk: 0,
			specs:    nil,
			expError: "have more desired chunks (1) than specs (0)",
		},
		{
			name:      "singleSpecTooManyChunks",
			numChunks: 2, printChunk: 1,
			specs:    []string{"foo"},
			expError: "have more desired chunks (2) than specs (1)",
		},
		{
			name:      "multipleSpecTooManyChunks",
			numChunks: 3, printChunk: 1,
			specs:    []string{"foo", "bar"},
			expError: "have more desired chunks (3) than specs (2)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			re, err := createChunkRegexp(c.numChunks, c.printChunk, c.specs)
			if c.expError != "" {
				require.EqualError(t, err, c.expError)
			} else {
				require.NoError(t, err)
				require.Equal(t, c.expRE, re)
			}
		})
	}
}

func TestFindMinimalWordPrefixes(t *testing.T) {
	type spec struct {
		name        string
		specs       []string
		expPrefixes []string
	}

	cases := []spec{
		{
			name:        "empty",
			specs:       nil,
			expPrefixes: nil,
		},
		{
			name:        "singleSpec",
			specs:       []string{"foo"},
			expPrefixes: []string{"foo"},
		},
		{
			name:        "twoSpecsSingleWordPrefix",
			specs:       []string{"foo", "foo bar"},
			expPrefixes: []string{"foo"},
		},
		{
			name:        "twoMultiWordSpecsSingleWordPrefix",
			specs:       []string{"foo bar", "foo baz"},
			expPrefixes: []string{"foo"},
		},
		{
			name:        "twoMultiWordSpecsLongPrefix",
			specs:       []string{"foo bar", "foo bar baz"},
			expPrefixes: []string{"foo bar"},
		},
		{
			name:        "threeSpecsSingleWordPrefix",
			specs:       []string{"foo", "foo bar", "foo baz"},
			expPrefixes: []string{"foo"},
		},
		{
			name:        "multiplePrefixes",
			specs:       []string{"foo", "foo bar", "foo bar baz", "bar foo", "baz buf", "baz bar foo"},
			expPrefixes: []string{"foo", "bar foo", "baz"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefixes := findMinimalWordPrefixes(c.specs)
			require.ElementsMatch(t, c.expPrefixes, prefixes)
		})
	}
}
