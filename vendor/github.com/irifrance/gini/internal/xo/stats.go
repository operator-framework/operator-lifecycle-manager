// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"fmt"
	"time"
)

type Stats struct {
	Start         time.Time
	Dur           time.Duration
	Vars          int
	Added         int64
	AddedLits     int64
	AddedUnits    int64
	AddedBinary   int64
	AddedTernary  int64
	Props         int64
	Sat           int64
	Unsat         int64
	Ended         int64
	Assumptions   int64
	Failed        int64
	Guesses       int64
	GuessRescales int64
	Conflicts     int64
	Learnts       int
	LearntLits    int64
	MinLits       int64
	Restarts      int64
	Compactions   int64
	Removed       int64
	RemovedLits   int64
	CDatGcs       int64
	CHeatRescales int64
	AddedBig      int64
	MaxTrail      int
	Pinned        int
	IncPinned     int
}

func (s *Stats) String() string {
	return fmt.Sprintf(`
c dur:                                %16s
c vars:                               %16d
c props:                              %16d
c added:                              %16d
c addedlits:                          %16d
c addedunits:                         %16d
c addedbinary:                        %16d
c addedternary:                       %16d
c addedbig:                           %16d
c sat:                                %16d
c unsat:                              %16d
c ended                               %16d
c assumptions:                        %16d
c failed:                             %16d
c guesses:                            %16d
c guessrescales:                      %16d
c conflicts:                          %16d
c learnts:                            %16d
c learntlits:                         %16d
c minLits:                            %16d
c restarts:                           %16d
c compactions:                        %16d
c removed:                            %16d
c removedlits:                        %16d
c cdatgcs:                            %16d
c cheatrescales:                      %16d
c maxtrail:                           %16d
c pinned:                             %16d
c incpinned:                          %16d`,
		s.Dur, s.Vars, s.Props, s.Added, s.AddedLits, s.AddedUnits, s.AddedBinary, s.AddedTernary,
		s.AddedBig, s.Sat, s.Unsat, s.Ended, s.Assumptions, s.Failed,
		s.Guesses, s.GuessRescales, s.Conflicts, s.Learnts, s.LearntLits,
		s.MinLits, s.Restarts, s.Compactions, s.Removed, s.RemovedLits, s.CDatGcs,
		s.CHeatRescales, s.MaxTrail, s.Pinned, s.IncPinned)
}

func NewStats() *Stats {
	s := &Stats{}
	s.Reset()
	return s
}

func (s *Stats) Reset() {
	s.Start = time.Now()
	s.Dur = 0 * time.Millisecond
	s.Vars = 0
	s.Added = 0
	s.AddedLits = 0
	s.AddedUnits = 0
	s.AddedBinary = 0
	s.AddedTernary = 0
	s.AddedBig = 0
	s.Props = 0
	s.Sat = 0
	s.Unsat = 0
	s.Ended = 0
	s.Assumptions = 0
	s.Failed = 0
	s.Guesses = 0
	s.GuessRescales = 0
	s.Conflicts = 0
	s.Learnts = 0
	s.LearntLits = 0
	s.MinLits = 0
	s.Restarts = 0
	s.Compactions = 0
	s.Removed = 0
	s.RemovedLits = 0
	s.CDatGcs = 0
	s.CHeatRescales = 0
	s.MaxTrail = 0
	s.Pinned = 0
	s.IncPinned = 0
}

func (s *Stats) Accumulate(t *Stats) {
	t.Dur = time.Since(t.Start)
	s.Dur += t.Dur
	s.Vars = t.Vars
	s.Added += t.Added
	s.AddedLits += t.AddedLits
	s.AddedUnits += t.AddedUnits
	s.AddedBinary += t.AddedBinary
	s.AddedTernary += t.AddedTernary
	s.AddedBig += t.AddedBig
	s.Props += t.Props
	s.Sat += t.Sat
	s.Unsat += t.Unsat
	s.Ended += t.Ended
	s.Assumptions += t.Assumptions
	s.Failed += t.Failed
	s.Guesses += t.Guesses
	s.GuessRescales += t.GuessRescales
	s.Conflicts += t.Conflicts
	s.Learnts = t.Learnts
	s.LearntLits = t.LearntLits
	s.MinLits = t.MinLits
	s.Restarts += t.Restarts
	s.Compactions += t.Compactions
	s.Removed += t.Removed
	s.RemovedLits += t.RemovedLits
	s.CDatGcs += t.CDatGcs
	s.CHeatRescales += t.CHeatRescales
	if s.MaxTrail < t.MaxTrail {
		s.MaxTrail = t.MaxTrail
	}
	s.Pinned = t.Pinned
	s.IncPinned = t.IncPinned
}
