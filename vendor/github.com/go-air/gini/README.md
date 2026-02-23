# ⊧ Gini: A fast SAT solver.

The Gini sat solver is a fast, clean SAT solver written in Go. It is to our knowledge
the first ever performant pure-Go SAT solver made available.


[![GoDoc](https://godoc.org/github.com/go-air/gini?status.svg)](https://godoc.org/github.com/go-air/gini)

[Google Group](https://groups.google.com/d/forum/ginisat) 

This solver is fully open source, originally developped at IRI France.

## Build/Install

For the impatient:

    go install github.com/go-air/gini/...@latest

I recommend however building the package github.com/go-air/gini/internal/xo with bounds checking
turned off.  This package is all about anything-goes performance and is the workhorse behind most of
the gini sat solver.  It is also extensively tested and well benchmarked, so it should not pose any
safety threat to client code.  This makes a signficant speed difference (maybe 10%) on long running
problems.

## The SAT problem in 5 minutes

[The SAT Problem](docs/satprob.md)

## Usage

Our [user guide](docs/manual.md) shows how to solve SAT problems, circuits, do Boolean optimisation,
use concurrency, using our distributed CRISP protocol, and more.


## Citing Gini

Zenodo DOI based citations and download:
[![DOI](https://zenodo.org/badge/64034957.svg)](https://zenodo.org/badge/latestdoi/64034957)

BibText:
```
@misc{scott_cotton_2019_2553490,
  author       = {Scott  Cotton},
  title        = {go-air/gini: Sapeur},
  month        = jan,
  year         = 2019,
  doi          = {10.5281/zenodo.2553490},
  url          = {https://doi.org/10.5281/zenodo.2553490}
}
```

