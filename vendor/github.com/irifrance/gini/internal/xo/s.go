// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"fmt"
	"io"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/irifrance/gini/dimacs"
	"github.com/irifrance/gini/inter"
	"github.com/irifrance/gini/z"
)

const (
	// for each Solve() call don't restart until this many conflicts.
	// good for incremental solving.
	RestartAfter  uint  = 1000
	RestartFactor       = 768
	PropTick      int64 = 20000
	CancelTicks   int64 = 1
)

// Solver implements a CDCL like solver with
// some performance bells and whistles
type S struct {
	Vars   *Vars
	Cdb    *Cdb
	Trail  *Trail
	Guess  *Guess
	Driver *Deriver
	Active *Active
	gmu    sync.Mutex
	rmu    sync.Mutex
	luby   *Luby

	// last conflict clause
	x z.C
	// if trivially inconsistent assumptions, first conflicting assumption
	xLit z.Lit

	// keeps level of start of each test (before assumptions)
	testLevels   []int
	endTestLevel int
	// assumptionLevel can be > endTestLevel for untested assumptions
	assumptLevel int
	assumes      []z.Lit // only last set of requested assumptions before solve/test.
	failed       []z.Lit
	phases       phases

	// Control
	control          *Ctl
	restartStopwatch int
	startTime        time.Time
	deadline         time.Time // synchronous (no pause)

	// Stats (each object has its own, read by ReadStats())
	stRestarts  int64
	stSat       int64
	stUnsat     int64
	stEnded     int64
	stPinned    int
	stIncPinned int
	stAssumes   int64
	stFailed    int64
}

// NewS creates a new Solver with default (relatively small) capacity
func NewS() *S {
	_ = z.LitNull
	return NewSVc(128, 768)
}

// NewSV creates a new Solver with specified capacity hint for
// the number of variables.
func NewSV(vCapHint int) *S {
	return NewSVc(vCapHint, vCapHint*8)
}

// NewSVc creates a new solver using specified capacity
// hints for number of variables (vCapHint) and number of clauses (cCapHint).
func NewSVc(vCapHint, cCapHint int) *S {
	vars := NewVars(vCapHint)
	cdb := NewCdb(vars, cCapHint)
	return NewSCdb(cdb)
}

// NewSDimacs creates a new S from a dimacs file.
func NewSDimacs(r io.Reader) (*S, error) {
	vis := &DimacsVis{}
	if e := dimacs.ReadCnf(r, vis); e != nil {
		return nil, fmt.Errorf("error reading dimacs: %s\n", e)
	}
	return vis.S(), nil
}

// Func NewSCdb creates a new Solver from a Cdb
func NewSCdb(cdb *Cdb) *S {
	vars := cdb.Vars
	guess := NewGuessCdb(cdb)
	trail := NewTrail(cdb, guess)
	drv := NewDeriver(cdb, guess, trail)
	s := &S{
		Vars:   vars,
		Cdb:    cdb,
		Trail:  trail,
		Guess:  guess,
		Driver: drv,
		luby:   NewLuby(),
		x:      CNull,
		xLit:   z.LitNull,

		testLevels: make([]int, 0, 128),

		assumptLevel: 0,
		assumes:      make([]z.Lit, 0, 1024),
		failed:       make([]z.Lit, 0, 3),

		restartStopwatch: 0,
		control:          NewCtl(nil)}
	s.control.stFunc = func(st *Stats) *Stats {
		s.ReadStats(st)
		return st
	}
	s.control.xo = s
	s.startTime = time.Now()
	s.deadline = s.startTime
	return s
}

func (s *S) Copy() *S {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	other := &S{}
	other.Vars = s.Vars.Copy()
	other.Cdb = s.Cdb.CopyWith(other.Vars)
	other.Guess = s.Guess.Copy()
	other.Trail = s.Trail.CopyWith(other.Cdb, other.Guess)
	other.Driver = s.Driver.CopyWith(other.Cdb, other.Guess, other.Trail)
	if s.Active != nil {
		other.Active = s.Active.Copy()
		other.Cdb.Active = other.Active
	}
	other.phases = s.phases
	luby := NewLuby()
	*luby = *(s.luby)
	other.luby = luby
	other.x = s.x
	other.xLit = s.xLit
	other.testLevels = make([]int, len(s.testLevels), cap(s.testLevels))
	copy(other.testLevels, s.testLevels)
	other.endTestLevel = s.endTestLevel
	other.assumptLevel = s.assumptLevel
	other.assumes = make([]z.Lit, len(s.assumes), cap(s.assumes))
	copy(other.assumes, s.assumes)
	other.failed = make([]z.Lit, len(s.failed), cap(s.failed))
	copy(other.failed, s.failed)
	other.restartStopwatch = s.restartStopwatch
	other.control = NewCtl(other)
	other.control.stFunc = func(st *Stats) *Stats {
		other.ReadStats(st)
		return st
	}
	other.startTime = s.startTime
	other.deadline = s.deadline
	return other
}

func (s *S) SCopy() inter.S {
	return s.Copy()
}

// GoSolve provides a connection to
// Solve() running in another goroutine.
func (s *S) GoSolve() inter.Solve {
	go func() {
		s.control.cResult <- s.Solve()
	}()
	return s.control
}

func (s *S) String() string {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	return fmt.Sprintf("<xo@%d>", s.Trail.Level)
}

func (s *S) Try(dur time.Duration) int {
	if dur <= 0 {
		return 0
	}
	s.startTime = time.Now()
	s.deadline = s.startTime.Add(dur)
	return s.Solve()
}

// Method Solve solves the problem added to the solver under
// assumptions specified by Assume.
//
// Solve returns -1 if unsat and 1 if sat
func (s *S) Solve() int {
	s.lock()
	defer s.unlock()
	defer func() {
		s.assumptLevel = 0
		s.assumes = s.assumes[:0]
	}()
	trail := s.Trail
	if r := s.solveInit(); r != 0 {
		return r
	}
	vars := s.Vars
	guess := s.Guess
	guess.nextRestart(s.restartStopwatch)
	driver := s.Driver
	cdb := s.Cdb
	aLevel := s.assumptLevel
	var x z.C
	nxtTick := trail.Props + PropTick
	tick := int64(0)

	for {
		x = trail.Prop()
		if x != CNull {
			// conflict
			if trail.Level <= aLevel {
				s.x = x
				s.stUnsat++
				return -1
			}
			drvd := driver.Derive(x)
			if drvd.TargetLevel < aLevel {
				trail.Back(aLevel)
			} else {
				trail.Back(drvd.TargetLevel)
			}
			trail.Assign(drvd.Unit, drvd.P)
			guess.Decay()
			cdb.Decay()
			if drvd.TargetLevel == 0 {
				s.stPinned = trail.Tail
			} else if drvd.TargetLevel <= aLevel {
				s.stIncPinned = trail.Tail
			}
			s.restartStopwatch--
			continue
		}

		// propagation ticker
		if trail.Props > nxtTick {
			nxtTick += PropTick
			tick++
			if tick%CancelTicks == 0 {
				if s.deadline != s.startTime && time.Until(s.deadline) <= 0 {
					return 0
				}
				if !s.control.Tick() {
					s.stEnded++
					trail.Back(s.endTestLevel)
					return 0
				}
			}
		}

		// maybe restart.
		if s.restartStopwatch <= 0 {
			nxt := s.luby.Next()
			s.restartStopwatch = int(nxt * RestartFactor)
			trail.Back(s.assumptLevel)
			s.stRestarts++
			guess.nextRestart(s.restartStopwatch)
		}

		// guess
		m := guess.Guess(vars.Vals)
		if m == z.LitNull {
			errs := cdb.CheckModel()
			if s.Active != nil {
				s.Cdb.checkModel = false
			}
			if len(errs) != 0 {
				for _, e := range errs {
					log.Println(e)
				}
				log.Println(s.Vars)
				log.Println(s.Trail)

				log.Fatalf("%p %p internal error in solve: sat model\n", s, s.control)
			}
			s.stSat++
			//fmt.Printf("vars %s\n", s.Vars)
			return 1
		}
		if u, c, ms := cdb.MaybeCompact(); u != 0 {
			_ = ms
			_ = c
			//log.Printf("compacted %d/%d/%d\n", u, c, ms)
		}
		trail.Assign(m, CNull)
	}
}

// Value retrieves the value of the literal m
func (s *S) Value(m z.Lit) bool {
	return s.Vars.Vals[m] == 1
}

// Test checks if the solver is consistent under unit propagation
// for the current assumptions and clauses and Test opens a scope for
// subsequent assumptions.  Test takes one argument, which is a slice of
// literals in which to put propagated literals or failed assumptions.
//
// Test returns a pair
//
//  (res, ns)
//
// where
//
//  - If res == 1, then the problem is SAT and a full model is found.
//  - If res == 0, then the problem is unknown (but consistent under unit
//    propagation).
//  - If res == -1 then the problem is UNSAT.
//
// Additionally if ns is not nil, then
//
//  - If res in {0,1}, then ns
//  contains all literals assigned under BCP since last Test(), including
//  assumptions.
//  - If res = -1, then ns contains the literals in a clause
//  which is false under unit propagation, or an assumed false literal, whichever
//  we happen upon first. (This is distinct from Why which gives failed literals).
//  - ns is stored in ms if possible
//
// Test operates under the following caveat.
//
//  If Test() or Solve() returns unsat, then Test() should not
//  be called subsequently until Untest() is called and returns 0.  Note
//  That it can be that Untest() will never return 0 if the problem is unsat
//  without assumptions.
//
// If this does not hold, Test panics.
//
func (s *S) Test(ms []z.Lit) (res int, ns []z.Lit) {
	//fmt.Printf("test\n")
	ns = ms
	if ns != nil {
		ns = ns[:0]
	}
	if x := s.cleanupSolve(); x != CNull {
		panic("test after unresolved unsat.")
	}
	res = 0
	s.testLevels = append(s.testLevels, s.Trail.Level)

	trail := s.Trail
	start := trail.Tail
	if r := s.makeAssumptions(); r == -1 {
		ns = nil
		res = -1
		return
	}
	end := trail.Tail
	s.endTestLevel = trail.Level
	if ns != nil {
		for i := start; i != end; i++ {
			ns = append(ns, trail.D[i])
		}
	}
	if !s.Guess.has(s.Vars.Vals) {
		errs := s.Cdb.CheckModel()
		if len(errs) != 0 {
			for _, e := range errs {
				log.Println(e)
			}
			log.Fatal("internal error test: sat model")
		}
		s.stSat++
		return 1, ns
	}
	return 0, ns
}

// Untest removes assumptions since last test and returns -1 if the solver is
// inconsistent under unit propagation after removing assumptions,  0
// otherwise.  If Untest is called and there is no corresponding Test(), then
// Untest panics.
//
// Note that when interleaving Solve,Test,and Untest, the
// following sequences are possible:
//
//  Assume(A1)
//  Test()     ->   0, []
//  Assume(A2)
//  Test()     ->   0
//  Assume(A3)
//  Solve()        -1
//  Untest()   ->  -1, [] // problem is unsat with A1 and A2
//  Untest()   ->  -1, [] // problem is unsat with A1 under BCP, even though it wasn't before
//
//  Assume(A1)
//  Test()     ->   0, [...]
//  Solve()    ->  -1 // unsat under A1
//  Untest()   ->   0
//  Assume(A2) ->
//  Test()     ->   1  // sat under A2
//  Untest()   ->   0
//  Assume(A1)
//  Test()     ->  -1, [] // problem is unsat with A1 under BCP, even though it wasn't before
func (s *S) Untest() int {
	if len(s.testLevels) == 0 {
		panic("Untest without Test")
	}
	trail := s.Trail
	lastTestLevel := s.lastTestLevel()
	s.testLevels = s.testLevels[:len(s.testLevels)-1]
	s.endTestLevel = lastTestLevel
	if x := s.cleanupSolve(); x != CNull {
		s.x = x
		return -1
	}
	s.Trail.backWithLates(lastTestLevel)
	if x := trail.Prop(); x != CNull {
		s.x = x
		return -1
	}
	s.x = CNull
	s.xLit = z.LitNull
	return 0
}

// Reasons returns the Reasons for a propagated literal
// returned from test.
//
// Reasons takes 2 arguments,
//  dst a z.Lit slice in which to place the reasons
//  m the literal for which to supply reasons.
//
// Reasons returns the reasons for m appended to
// dst.
//
// If m is not a propagated literal returned from
// Test() (without Untest() in between), the result
// is undefined and may panic.
func (s *S) Reasons(dst []z.Lit, m z.Lit) []z.Lit {
	dst = dst[:0]
	p := s.Vars.Reasons[m.Var()]
	if p == CNull {
		return dst
	}
	D := s.Cdb.CDat.D
	p++ // invariant that implied reasons are always first in clause
	for {
		r := D[p]
		if r == z.LitNull {
			break
		}
		// invariant m should be the first.
		dst = append(dst, r.Not())
		p++
	}
	return dst
}

// ReadStats reads data from the solver into st.  The solver values are reset
// if they are cumulative.  The duration and start time attributes of st are
// not touched.
func (s *S) ReadStats(st *Stats) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	st.Restarts += s.stRestarts
	s.stRestarts = 0
	st.Sat += s.stSat
	s.stSat = 0
	st.Unsat += s.stUnsat
	s.stUnsat = 0
	st.Ended += s.stEnded
	s.stEnded = 0
	st.Pinned = s.stPinned
	st.IncPinned = s.stIncPinned
	st.Assumptions += s.stAssumes
	s.stAssumes = 0
	st.Failed += s.stFailed
	s.stFailed = 0
	s.Vars.readStats(st)
	s.Trail.readStats(st)
	s.Guess.readStats(st)
	s.Driver.readStats(st)
	s.Cdb.readStats(st)
}

// Add implements inter.S
func (s *S) Add(m z.Lit) {
	s.ensureLitCap(m)
	if m == z.LitNull {
		s.ensure0()
		s.Cdb.checkModel = true
	}
	loc, u := s.Cdb.Add(m)
	if u != z.LitNull {
		s.Trail.Assign(u, loc)
	}
}

func (s *S) ensureActive() {
	if s.Active == nil {
		s.Active = newActive(int(s.Vars.Top))
		s.Cdb.Active = s.Active
	}
}

func (s *S) Activate() z.Lit {
	s.ensure0()
	s.ensureActive()
	m := s.Active.Lit(s)
	s.Active.ActivateWith(m, s)
	return m
}

func (s *S) ActivationLit() z.Lit {
	s.ensure0()
	s.ensureActive()
	return s.Active.Lit(s)
}

func (s *S) ActivateWith(act z.Lit) {
	s.ensure0()
	s.ensureActive()
	s.Active.ActivateWith(act, s)
}

func (s *S) Deactivate(m z.Lit) {
	s.ensure0()
	s.ensureActive()
	s.Active.Deactivate(s.Cdb, m)
}

func (s *S) ensure0() {
	if len(s.testLevels) != 0 {
		panic("ivalid operation under test scope")
	}
	if s.Trail.Level != 0 {
		s.Trail.Back(0)
	}
	s.x = CNull
	s.xLit = z.LitNull
	s.failed = nil
}

// Assume causes the solver to Assume the literal m to be true for the
// next call to Solve() or Test().
//
// This may be called multiple times, indicating to make multiple assumptions.
// The assumptions hold for the next call to Solve() or Test().  Afterwards, if
// the result is unsat, then s.Why() gives a subset of inconsistent assumptions.
//
// When used with sequences of Test/Untest and Solves, S makes a distinction between
// tested and untested assumptions.  Solve always forgets/consumes untested assumptions;
// but Solve never forgets/consumes tested assumptions.  Forgetting tested assumptions
// is accomplished with s.Untest().
func (s *S) Assume(ms ...z.Lit) {
	s.stAssumes += int64(len(ms))
	s.assumes = append(s.assumes, ms...)
	for _, m := range ms {
		s.ensureLitCap(m)
	}
}

// Who identifies the solver and configuration.
func (s *S) Who() string {
	return fmt.Sprintf("xo.S %s/%s/%d", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}

// MaxVar returns the maximum variable added or assumed.
func (s *S) MaxVar() z.Var {
	return s.Vars.Max
}

// Why appends to ms a minimized list of assumptions
// which together caused previous call to be unsat.
//
// If previous call was not unsat, then Why() returns ms
func (s *S) Why(ms []z.Lit) []z.Lit {
	s.failed = ms
	if s.xLit != z.LitNull {
		s.failed = append(s.failed, s.xLit)
		s.final([]z.Lit{s.xLit})
	} else if s.x != CNull {
		s.final(s.Cdb.Lits(s.x, nil))
	} else {
		return ms
	}
	return s.failed
}

// returns -1 if known to be inconsistent by BCP
// 0 otherwise.
func (s *S) solveInit() int {

	// set up restarts TODO(wsc) optimize this
	s.luby = NewLuby()
	for {
		r := s.luby.Next() * RestartFactor
		if r >= RestartAfter {
			s.restartStopwatch = int(r)
			break
		}
	}
	if x := s.cleanupSolve(); x != CNull {
		s.x = x
		return -1
	}

	//log.Printf("%s\n", s.Trail)
	//log.Printf("%s\n", s.Vars)
	// make any new assumptions
	if r := s.makeAssumptions(); r == -1 {
		return r
	}
	//log.Printf("%s\n", s.Trail)
	//log.Printf("%s\n", s.Vars)

	// initialize phase
	s.phases = s.phases.init(s)
	return 0
}

// can fail to clean up if a a unit propagates
// to a level properly lower than the highest
// test scope and the highest test scope
// contains the negation of that unit.  pass
// this info to caller in the form of return
// value for placing in s.x, or panicking if
// it indicates an invalid usage, such as a
// test after an unresolved unsat.
func (s *S) cleanupSolve() z.C {
	trail := s.Trail
	for s.x != CNull {
		if s.Cdb.Bot != CNull { // Cdb.Bot is always checked in makeAssumptions, true empty clause.
			s.x = CNull
			break
		}
		drvd := s.Driver.Derive(s.x)
		if drvd.TargetLevel < s.endTestLevel {
			trail.Back(s.endTestLevel)
			if s.Vars.Levels[drvd.Unit.Var()] == -1 {
				trail.Assign(drvd.Unit, drvd.P)
				s.x = trail.Prop()
				continue
			}
			return drvd.P
		}
		trail.Back(drvd.TargetLevel)
		trail.Assign(drvd.Unit, drvd.P)
		s.x = trail.Prop()
		continue
	}
	trail.Back(s.endTestLevel)
	s.xLit = z.LitNull
	s.failed = nil
	return CNull
}

func (s *S) lastTestLevel() int {
	if len(s.testLevels) > 0 {
		return s.testLevels[len(s.testLevels)-1]
	}
	return 0
}

func (s *S) makeAssumptions() int {
	// make assumptions
	trail := s.Trail
	s.stAssumes += int64(len(s.assumes))
	s.assumptLevel = trail.Level
	s.stPinned = trail.Tail
	defer func() {
		s.assumes = s.assumes[:0]
	}()
	// check if consistent without assumptions
	if s.Cdb.Bot != CNull {
		s.x = s.Cdb.Bot
		return -1
	}
	if x := trail.Prop(); x != CNull {
		s.x = x
		return -1
	}
	vals := s.Vars.Vals
	for _, m := range s.assumes {
		switch vals[m] {
		case 0:
			s.assumptLevel++
			trail.Assign(m, CNull)
			if x := trail.Prop(); x != CNull {
				s.x = x
				return -1
			}
			s.stIncPinned = trail.Tail
		case 1:
			// nothing
		case -1:
			s.xLit = m
			s.stFailed++
			return -1
		default:
			panic(fmt.Sprintf("bad value %d\n", vals[m]))
		}
	}
	return 0
}

func (s *S) final(ms []z.Lit) {
	marks := make([]bool, s.Vars.Max+1)
	for _, m := range ms {
		s.finalRec(m, marks)
	}
	return
}

// FinalRec computes the assumptions which caused
// the problem to be unsat (causality here is wrt bcp)
// and records them in s.Failed
func (s *S) finalRec(m z.Lit, marks []bool) {
	if marks[m.Var()] {
		return
	}
	marks[m.Var()] = true

	r := s.Vars.Reasons[m.Var()]
	if r == CNull {
		s.failed = append(s.failed, m.Not())
		s.stFailed++
		return
	}
	D := s.Cdb.CDat.D
	for r = r + 1; ; r++ {
		n := D[r]
		if n == z.LitNull {
			break
		}
		s.finalRec(n, marks)
	}
	return
}

// Lit returns the positive literal of a fresh variable.
func (s *S) Lit() z.Lit {
	n := s.Vars.Max + 1
	m := n.Pos()
	s.ensureLitCap(m)
	return m
}

// we keep a global track of variable/literal capacity here.
// when we need to grow, all subcomponents grow.
func (s *S) ensureLitCap(m z.Lit) {
	vars := s.Vars
	mVar := m.Var()
	top := vars.Top
	grow := mVar >= top
	if grow {
		for top <= mVar {
			top *= 2
		}
		vars.growToVar(top)
		s.Cdb.growToVar(top)
		s.Trail.growToVar(top)
		s.Guess.growToVar(top)
		s.Driver.growToVar(top)
		if s.Active != nil {
			s.Active.growToVar(top)
		}
	}
	if mVar > vars.Max {
		for i := vars.Max + 1; i <= mVar; i++ {
			s.Guess.Push(i.Pos())
		}
		vars.Max = mVar
	}
}

func (s *S) lock() {
	s.gmu.Lock()
	s.rmu.Lock()
}

func (s *S) unlock() {
	s.gmu.Unlock()
	s.rmu.Unlock()
}
