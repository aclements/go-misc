// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// issue16083 is a model for finding how mark can complete when stack
// scans are still in progress.
//
// XXX Move gcMarkRootCheck to after forEachP to force final workers
// out?
package main

import (
	"fmt"

	"github.com/aclements/go-misc/go-weave/amb"
	"github.com/aclements/go-misc/go-weave/weave"
)

var sched = weave.Scheduler{Strategy: &amb.StrategyRandom{}}

type State struct {
	workers      weave.AtomicInt32
	markrootNext weave.AtomicInt32
	markrootJobs int32
	scanned      [2]weave.AtomicInt32

	markDoneSema weave.Mutex

	done bool
}

func main() {
	sched.Run(func() {
		var s State
		s.markrootJobs = int32(len(s.scanned))

		for i := 0; i < 4; i++ {
			sched.Go(s.worker)
		}
	})
}

func (s *State) worker() {
	// This has a liveness problem, so limit it to 4 iterations.
	for i := 0; i < 3 && !s.done; i++ {
		sched.Trace("s.workers++")
		n := s.workers.Add(+1)
		// XXX This trace appears in the wrong place since Add
		// did a Sched after the modification. Perhaps we
		// should pre-Sched? Or I could put this in an atomic block.
		sched.Tracef(" => %d", n)

		s.gcDrain()

		sched.Trace("s.workers--")
		n = s.workers.Add(-1)
		sched.Tracef(" => %d", n)

		if n == 0 {
			sched.Tracef("s.workers == 0")
			if !s.gcMarkWorkAvailable() {
				sched.Tracef("!gcMarkWorkAvailable()")
				s.gcMarkDone()
			}
		}
	}
	sched.Trace("exit")
}

func (s *State) gcDrain() {
	job := s.markrootNext.Add(1) - 1
	if job < s.markrootJobs {
		sched.Tracef("scanning %d", job)
		s.scanned[job].Store(1)
	}
}

func (s *State) gcMarkWorkAvailable() bool {
	return s.markrootNext.Load() < s.markrootJobs
}

func (s *State) gcMarkDone() {
	s.markDoneSema.Lock()
	defer s.markDoneSema.Unlock()

	if !(s.workers.Load() == 0 && !s.gcMarkWorkAvailable()) {
		sched.Tracef("gcMarkDone retry")
		return
	}

	s.gcMarkRootCheck()

	s.done = true
}

func (s *State) gcMarkRootCheck() {
	for i := range s.scanned {
		if s.scanned[i].Load() == 0 {
			panic(fmt.Sprintf("missed %d", i))
		}
	}
}
