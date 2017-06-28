// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

import (
	"errors"
	"fmt"

	"github.com/aclements/go-misc/go-weave/amb"
)

// TODO: Implement simple partial order reduction. Group all pending
// read operations and schedule them as a single unit.

// TODO: Implement a PCT scheduler (https://www.microsoft.com/en-us/research/publication/a-randomized-scheduler-with-probabilistic-guarantees-of-finding-bugs/)

type Scheduler struct {
	Strategy amb.Strategy

	as amb.Scheduler

	nextid    int
	runnable  []*thread
	blocked   []*thread
	curThread *thread
	goErr     interface{}

	// wakeSched wakes the scheduler to select the next thread to
	// run. The waking thread must immediately block on
	// thread.wake or exit.
	wakeSched chan void

	trace []traceEntry
}

var globalSched *Scheduler

type void struct{}

type thread struct {
	sched   *Scheduler
	id      int
	index   int // Index in Scheduler.runnable or .blocked
	blocked bool

	tls map[*TLS]interface{}

	wake chan void // Send void{} to wake this thread
}

func (t *thread) String() string {
	return fmt.Sprintf("T%d", t.id)
}

const debug = false

func (s *Scheduler) newThread() *thread {
	thr := &thread{s, s.nextid, -1, false, nil, make(chan void)}
	s.nextid++
	if thr.id != -1 {
		thr.index = len(s.runnable)
		s.runnable = append(s.runnable, thr)
	}
	return thr
}

func (s *Scheduler) Run(main func()) {
	if globalSched != nil {
		panic("only one weave.Scheduler can be active at a time")
	}
	globalSched = s
	defer func() { globalSched = nil }()

	s.as = amb.Scheduler{Strategy: s.Strategy}

	s.as.Run(func() {
		// Initialize state.
		s.nextid = 0
		s.runnable = s.runnable[:0]
		s.blocked = s.blocked[:0]
		s.curThread = nil
		s.goErr = nil
		s.wakeSched = make(chan void)
		s.trace = nil
		s.goNoSched(main)
		s.scheduler()
		if s.goErr != nil {
			panic(errorWithTrace{s.goErr, s.trace})
		}
		if len(s.blocked) != 0 {
			panic(errorWithTrace{fmt.Sprintf("threads asleep: %s", s.blocked), s.trace})
		}
		if debug {
			fmt.Println("run done")
		}
	})
}

func (s *Scheduler) goNoSched(f func()) {
	thr := s.newThread()
	go func() {
		defer func() {
			goErr := recover()

			if debug {
				if goErr == threadAbort {
					fmt.Printf("%v aborted\n", thr)
				} else if goErr != nil {
					fmt.Printf("%v panicked: %v\n", thr, goErr)
				} else {
					fmt.Printf("%v exiting normally\n", thr)
				}
			}

			// Remove this thread from runnable.
			s.runnable[thr.index] = s.runnable[len(s.runnable)-1]
			s.runnable[thr.index].index = thr.index
			s.runnable = s.runnable[:len(s.runnable)-1]

			// If this is a thread abort, notify the
			// scheduler that we're done aborting and
			// exit.
			if goErr == threadAbort {
				s.wakeSched <- void{}
				return
			}

			// If we're panicking, report the error so the
			// scheduler can shut down this execution.
			//
			// TODO: Capture the stack trace.
			if goErr != nil {
				if s.goErr == nil {
					s.goErr = goErr
				}
				s.wakeSched <- void{}
				return
			}

			// Otherwise, this is a regular thread exit.
			close(thr.wake)
			s.wakeSched <- void{}
		}()
		if debug {
			fmt.Printf("%v started\n", thr)
		}
		thr.desched()
		f()
	}()
}

func (s *Scheduler) Go(f func()) {
	s.goNoSched(f)
	s.Sched()
}

var threadAbort = errors.New("thread aborted because of panic in another thread")

// scheduler runs on the top-level thread and coordinates which thread
// to execute next.
func (s *Scheduler) scheduler() {
	for len(s.runnable) > 0 {
		// Pick a thread to run. If we're aborting, we just
		// pick runnable[0], since it's not useful to explore
		// this, and we might be aborting because amb
		// terminated this path anyway.
		var tid int
		if s.goErr == nil {
			// Amb may panic with PathTerminated.
			func() {
				defer func() {
					err := recover()
					if err == amb.PathTerminated {
						s.goErr = err
					} else if err != nil {
						panic(err)
					}
				}()
				tid = s.as.Amb(len(s.runnable))
			}()
		}
		s.curThread = s.runnable[tid]

		if debug {
			fmt.Printf("scheduling %v from %v\n", s.curThread, s.runnable)
		}

		// Switch to that thread.
		s.curThread.wake <- void{}

		// Wait for thread to deschedule.
		<-s.wakeSched
		if s.goErr != nil {
			// This state will signal all threads to exit,
			// but we have to wake blocked threads so they
			// can exit, too.
			s.runnable = append(s.runnable, s.blocked...)
			s.blocked = nil
		}
	}
}

func (s *Scheduler) Sched() {
	this := s.curThread
	s.wakeSched <- void{}
	this.desched()
}

func (t *thread) desched() {
	<-t.wake
	if t.sched.goErr != nil {
		// We're shutting down this execution.
		panic(threadAbort)
	}
}

func (s *Scheduler) Amb(n int) int {
	return s.as.Amb(n)
}

func (t *thread) block(abortf func()) {
	if t.blocked {
		panic("thread blocked multiple times")
	}
	t.blocked = true

	s := t.sched
	s.runnable[t.index] = s.runnable[len(s.runnable)-1]
	s.runnable[t.index].index = t.index
	s.runnable = s.runnable[:len(s.runnable)-1]

	t.index = len(s.blocked)
	s.blocked = append(s.blocked, t)

	if abortf != nil {
		defer func() {
			if abortf != nil {
				abortf()
			}
		}()
	}
	t.sched.Sched()
	abortf = nil
}

func (t *thread) unblock() {
	if !t.blocked {
		panic("thread unblocked while not blocked")
	}
	t.blocked = false

	s := t.sched
	s.blocked[t.index] = s.blocked[len(s.blocked)-1]
	s.blocked[t.index].index = t.index
	s.blocked = s.blocked[:len(s.blocked)-1]

	t.index = len(s.runnable)
	s.runnable = append(s.runnable, t)
}
