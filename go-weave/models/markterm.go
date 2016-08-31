// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// markterm is a model for finding when mark termination can start
// before all work is drained in Go 1.7. This model is expected to
// fail.
package main

import (
	"fmt"

	"github.com/aclements/go-misc/go-weave/amb"
	"github.com/aclements/go-misc/go-weave/weave"
)

var sched = weave.Scheduler{Strategy: &amb.StrategyRandom{}}

type State struct {
	workers int
	grey    int
	done    bool
}

func main() {
	sched.Run(func() {
		var s State
		s.grey = 3

		for i := 0; i < 2; i++ {
			sched.Go(s.worker)
		}
	})
}

func (s *State) worker() {
	// TODO: This has a liveness problem: if a worker takes the
	// last work and then doesn't get scheduled again, the other
	// worker will spin. Curiously, duplicate state detection
	// would cut off that path, which means if we see a duplicate
	// with an earlier state in the same path (versus a different
	// path), it's a livelock and perhaps should be reported.
	for {
		s.workers++
		sched.Tracef("s.workers++ (%d)", s.workers)
		sched.Sched()

		s.check()

		switch {
		case s.grey <= 0:
			// Do nothing
		case s.grey == 1:
			// Pull one pointer, put none.
			s.grey--
			sched.Tracef("s.grey-- (%d)", s.grey)
			sched.Sched()
		default:
			// Remove two pointers, then add one to simulate
			// pulling a buffer off full and then putting one back
			// one full.
			s.grey -= 2
			sched.Tracef("s.grey -= 2 (%d)", s.grey)
			sched.Sched()
			s.grey++
			sched.Tracef("s.grey++ (%d)", s.grey)
			s.check()
			sched.Sched()
		}

		var grey int
		if true { // Read full list ("grey") before dec(&workers)
			grey = s.grey
			sched.Tracef("grey := s.grey (%d)", s.grey)
			sched.Sched()
		}

		s.workers--
		n := s.workers
		sched.Tracef("s.workers-- (%d)", s.workers)
		sched.Sched()

		if false {
			grey = s.grey
			sched.Tracef("grey := s.grey (%d)", s.grey)
			sched.Sched()
		}

		if n == 0 && grey == 0 {
			s.done = true
			sched.Trace("s.done = true")
			sched.Sched()
			s.check()
			break
		}
	}
	sched.Trace("exit")
}

func (s *State) check() {
	if s.done && s.grey > 0 {
		panic(fmt.Sprintf("done, but grey==%d", s.grey))
	}
}
