// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

// Package gini provides a fast SAT solver.
//
// Package gini contains both libraries and commands.  The libraries include
//
//  - A high quality, core single goroutine SAT solver (internal package xo).
//  - Concurrent solving utilities (gini/ax, ...)
//  - CRISP-1.0 client and server (gini/crisp)
//  - Generators (gini/gen)
//  - benchmarking library (gini/bench)
//  - scoped assumptions
//  - logic library
//  ...
//
// The commands include
//
//  - gini, a command for solving SAT problems in cnf and icnf formats, and a
//    CRISP-1.0 client.
//  - bench, a utility for constructing, running, and comparing benchmarks
//  - crispd, CRISP-1.0 server
package gini
