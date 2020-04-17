# Gini SAT Solver

The Gini sat solver is a fast, clean SAT solver written in Go. It is to our knowledge
the first ever performant pure-Go SAT solver made available.

| [![Build Status](https://travis-ci.org/irifrance/gini.svg?branch=master)](https://travis-ci.org/irifrance/gini) | [![GoDoc](https://godoc.org/github.com/irifrance/gini?status.svg)](https://godoc.org/github.com/irifrance/gini) | [Google Group](https://groups.google.com/d/forum/ginisat) |
------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------|

This solver is fully open source, originally developped at IRI France.


# Build/Install

For the impatient:

    go get github.com/irifrance/gini...

I recommend however building the package github.com/irifrance/gini/internal/xo with bounds checking
turned off.  This package is all about anything-goes performance and is the workhorse behind most of
the gini sat solver.  It is also extensively tested and well benchmarked, so it should not pose any
safety threat to client code.  This makes a signficant speed difference (maybe 10%) on long running
problems.


# The SAT Problem

The SAT problem is perhaps the most famous NP-complete problem.  As such, SAT
solvers can be used to try to solve hard problems, such as travelling salesman
or RSA cracking. In practice, many SAT problems are quite easy (but not
decryption problems...yet).  The solvers are used in software verification,
hardware verification and testing, AI planning, routing, etc.

The SAT problem is a Boolean problem.  All variables can either be true or
false, but nothing else.  The SAT problem solves systems of Boolean
constraints, called clauses.  Namely, SAT solvers work on conjunctive normal
form problems (CNFs).  There are many ways to efficiently code arbitrary logic
into CNF, so this is not so much a restricting factor.  Nonetheless, we present
CNF and the problem below in a brief self-contained fashion which we find
useful.  Readers interested in more depth should consult Wikipedia, or The
Handbook of Satisfiability, or Donald Knuth's latest volume of The Art of
Computer Programming.

## CNF
A CNF is a conjunction of clauses

    c1 and c2 and ... and cM

Each c[i], i in [1..M], is a clause, which is of the form

    m1 or m2 or ... or mN

where each m[i], i in [1..N] is either a Boolean variable (such as x), or the
negation of a Boolean variable (such as not(y)).  An expression which is either
a Boolean variable or its negation is called a "literal".

In the following, we refer to variables simply by integers 1,2,3,...

Clauses are often written in succint form

    -3 11 12 14 -257

Numerical negation indicates logical negation, and spaces are disjunctions
"or".  Sometimes "+" is used for "or".

Conjunctions are just concatenation of clauses.  We can parenthesize clauses
such as

    (1 -2) (2 -3) (3 -4) (4 -1)

which expresses a set of clauses whose satisfying assignments are

    {1,2,3,4}
        or
    {-1,-2,-3,-4}

## Models
A model of a CNF is a value for each of the variables which makes every clause 
in the CNF true.  The SAT problem is determining whether or not a model exists
for a given set of clauses.


## Proofs

### Resolution

Resolution is a form of logical reasoning with conjunctions of clauses.  Given
2 clauses of the form

    C + v
and

    D + -v

We can conclude that 

    C + D

must be true.

Here, C and D are arbitrary clauses.

Resolution proof of unsatisfiability is a derivation of the empty disjuction
(false) by means of resolution.  Resolution proofs, even minimally sized ones,
can be very large, exponentially larger than the input problem.

Modern SAT solvers mostly rely on performing operations which correspond to
bounded size (in terms of number of variables) number of resolutions.  Given
this fact together with the fact that the minimal proofs can be exponentially
large in the number of variables, some problems can take an exponential amount
of time.

Nonetheless, many SAT solvers have heuristics and are optimised so much that
even hard problems become tractable.  With up to several tens of millions of
resolutions happening per second on one modern single core CPU, even problems
with known exponential bounds on resolution steps can be solved.

# Solving Formulas and Circuits
Gini provides a simple and efficient logic modelling library which supports
easy construction of arbitrary Boolean formulas.  The library uses and-inverter
graphs, structural hashing, constant propagation and can be used for
constructing compact formulas with a rich set of Boolean operators.  The
circuit type implements an interface which makes it plug into a solver
automatically.  In fact, the circuit type uses the same representation for
literals as the solver, so there is no need to map between solver and circuit
variables.

Additionally, sequential circuits are supported.  The sequential part of the
logic library provides memory elements (latches) which are evaluated initially
as inputs and take a "next" value which provides input to the next cycle of the
circuit.  The library supports unrolling sequential circuits for a fixed number
of cycles to arrive at a non-sequential formula which can then be checked for
satisfiability using solving tools.

Gini also supports cardinality constraints which can constrain how many of a 
set of Boolean variables are true.  Cardinality constraints in turn provide
an easy means of doing optimisation.  Gini uses sorting networks to code
cardinality constraints into clauses.  Sorting networks are a good general
purpose means of handling cardinality constraints in a problem context which
also contains lots of purely Boolean logic (implicitly or not).

Most SAT use cases use a front end for modelling arbitrary formulas.  When formats
are needed for interchange, Gini supports the following.

## Aiger
Gini supports [aiger version 1.9](http://fmv.jku.at/aiger/) in conjunction
with its logic library.  The logic.C and logic.S circuit types can be 
stored, exchanged, read and written in aiger ascii and binary formats.

## Dimacs
CNF Dimacs files, which are an ancient widely used format for representing CNF
formulas.  Dimacs files are usually used for benchmarking solvers, to eliminate
the formula representation layer.  The fact that the format is more or less
universally supported amongst SAT solvers leads some SAT users to use this
format, even though there is I/O, CNF translation,  and parsing overhead by
comparison to using a logic library.


# Optimisation
With Cardinality constraints, optimisation is easy

    import "github.com/irifrance/gini"
    import "github.com/irifrance/gini/logic"

    c := logic.NewC()


    // suppose we encode package constraints for a module in the circuit c
    // and we have a slice S of dependent packages P each of which has an attribute
    // P.needsRepl which indicates whether or not it needs to be replaced (of type
    // github.com/irifrance/gini/z.Lit)

    repls := make([]z.Lit, 0, 1<<23)
    for _, p := range pkgs {
        repls = append(repls, p.needsRepl)
    }

    // make a cardinality constraints object
    cards := c.CardSort(repls)

    // loop through the constraints (note a linear search
    // can be faster than a binary search in this case because the underlying solver
    // often has locality of logic cache w.r.t. cardinality constraints)
    s := gini.New()
    c.ToCnf(s)
    minRepls := -1
    for i := range repls {
        s.Assume(cards.Leq(i))
        if s.Solve() == 1 {
            minRepls = i
            break
        }
    }

    // use the model, if one was found, from s to propose a build

# Activation Literals
Gini supports recycling activation literals with the 
[Activatable interface](http://godoc.org/github.com/irifrance/gini/inter#Activatable)

Even without recycling, activation literals provide an easy way to solve MAXSAT problems:
just activate each clause, use a cardinality constraint on the activation literals,
and then optimize the output.

With recycling, one can do much more, such as activating and deactivating sets of clauses,
and constructing the clauses on the fly.  Activations work underneath test scopes and
assumptions, making the interface for Gini perhaps the most flexible available.


# Performance
In applications, SAT problems normally have an exponential tail runtime
distribution with a strong bias towards bigger problems populating the longer
runtime part of the distribution.  So in practice, a good rule of thumb is 1 in
N problems will on average take longer than time alotted to solve it for a
problem of a given size, and then one measures N experimentally.  Very often,
despite the NP nature of SAT, an application can be designed to use a SAT solver
in a way that problems almost never take too long.  Additionally, the hardest known
hand-crafted problems for CDCL solvers which take significant time involve at least 
a few hundred variables.  So if you're application has only a few hundred variables,
you're probably not going to have any performance problems at all with any solver.

As in almost every solver, the core CDCL solver in Gini is the workhorse and is a 
good general purpose solver.  Some specific applications do benefit from 
pre- or in-processing, and some some applications may not be useable with such 
techniques.  Other solvers provide more and better pre- or in-processing than Gini
and help is welcome in adding such solving techniques to Gini.

The core CDCL solver in Gini has been compared with that in MiniSAT and PicoSAT,
two standard such solvers on randomly chosen SAT competition problems.  In this
evaluation, Gini out performed PicoSAT and was neck-in-neck with MiniSAT.  The
core CDCL solver in Gini also measures up to PicoSAT and MiniSAT in terms of
"propagations per second", indicating the core routines are themselves competitive
with these solvers, not only the heuristics.  This level of performance has not to 
our knowledge been achieved by other sat solvers in Go, such as go-sat or gophersat.

While the above evaluation casts a fairly wide net over application domains and
problem difficulty, the performance of sat solvers and underlying algorithms are 
fundamentally hard to predict in any rigorous way.  So your experience may differ,
but we are confident Gini's core solver is a well positioned alternative to standard
high-performance CDCL solvers in C/C++.  We encourage you to give it a try and welcome
any comparisons.

## Benchmarking
To that end, gini comes with a nifty SAT solver benchmarking tool which allows
to easily select benchmarks into a "bench" format, which is just a particular
structure of directories and files.  The tool can then also run solvers 
on such generated "bench"'s, enforcing various timeouts and logging all details,
again in a standard format.  To tool then can compare the results in various 
fashions, printing out scatter and cactus plots (in UTF8/ascii art) of various 
user selectable subsets of the benchmark run.

You may find this tool useful to fork and adopt to benchmark a new kind of
program.  The benchmarking mechanism is appropriate for any "solver" like
software (SMT, CPLEX, etc) where runtimes vary and are unpredictable and
potentially high.  If you do so, please follow the license or ask for
alternatives.

# Concurrency
Gini is written in Go and uses several goroutines by default for garbage
collection and system call scheduling.  There is a "core" single-goroutine
solver, xo, which is in an internal package for gutsy low level SAT hackers. 

## Connections to solving processes
Gini provides safe connections to solving processes which are guaranteed to not
lose any solution found, can pause and resume, run with a timeout, test without
solving, etc.

## Solve-time copyable solvers.
Gini provides copyable solvers, which can be safely copied *at solvetime during
a pause*.

## Ax
Gini provides an "Assumption eXchange" package for deploying solves
under different sets of assumptions to the same set of underlying constraints
in parallel. This can give linear speed up in tasks, such as PDR/IC3, which 
generate lots of assumptions.

We hope to extend this with clause sharing soon, which would give 
superlinear speedup according to the literature.

# Distributed and CRISP

Gini provides a definition and reference implementation for
[CRISP-1.0](https://github.com/irifrance/gini/blob/master/doc/crisp/crisp.pdf),
the compressed incremental SAT protocol.  The protocol is a client-server wire
protocol which can dispatch an incremental sat solver with very little overhead
as compared to direct API calls.  The advantage of using a protocol is that it
allows arbitrary tools to implement the solving on arbitrary hardware without
affecting the client.  

Many SAT applications are incremental and easily solve huge numbers of problems
while only a few problems are hard.  CRISP facilitates pulling out the big guns
for the hard problems while not affecting performance for easy problems.  Big
guns tend to be harder to integrate into applications because of compilation
issues, hardware requirements, size and complexity of the code base, etc.
Applications that use CRISP can truly isolate themselves from the woes of
integrating big guns while benefiting on hard problems.

CRISP also allows language independent incremental SAT solving.  The
applications and solvers can be readily implemented without the headache of
synchronizing programming language, compilers, or coding style.

We are planning on implementing some CRISP extensions, namely the multiplexing
interface which will enable (possibly remote) clients to control
programmatically partitioning or queuing of related SAT problems.

The CRISP protocol provides a basis for distributed solving.  Gini implements
a CRISP-1.0 client and server.  

A command, crispd, is supplied for the CRISP server.

