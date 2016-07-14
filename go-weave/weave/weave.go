// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

import (
	"errors"
	"fmt"

	"github.com/aclements/go-misc/go-weave/amb"
)

type Scheduler struct {
	Strategy amb.Strategy

	as amb.Scheduler

	nextid               int
	runnable             []*thread
	blocked              []*thread
	runThread, curThread *thread
	goErr                interface{}
}

var globalSched *Scheduler

type thread struct {
	sched   *Scheduler
	id      int
	index   int // Index in Scheduler.runnable or .blocked
	blocked bool
	wake    chan bool // Send true to continue, false to abort
	aborted chan bool
}

func (t *thread) String() string {
	return fmt.Sprintf("t%d", t.id)
}

const debug = false

func (s *Scheduler) newThread() *thread {
	thr := &thread{s, s.nextid, -1, false, make(chan bool), make(chan bool)}
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
		s.nextid = -1
		s.runnable = nil
		s.runThread = s.newThread()
		s.curThread = s.runThread
		s.goErr = nil
		s.Go(main)
		if goErr := s.goErr; goErr != nil {
			// Exit all threads. They should all be
			// stopped in desched right now. Do this
			// sequentially so defer blocks can clean up
			// sequentially.
			for _, thr := range s.runnable {
				thr.wake <- false
				<-thr.aborted
			}
			for _, thr := range s.blocked {
				thr.wake <- false
				<-thr.aborted
			}
			panic(goErr)
		}
		if debug {
			fmt.Println("run done")
		}
	})
}

func (s *Scheduler) Go(f func()) {
	thr := s.newThread()
	go func() {
		defer func() {
			goErr := recover()
			if goErr == threadAbort {
				if debug {
					fmt.Printf("%v aborted\n", thr)
				}
				thr.aborted <- true
				return
			}

			if debug {
				if goErr != nil {
					fmt.Printf("%v panicked: %v\n", thr, goErr)
				} else {
					fmt.Printf("%v exiting normally\n", thr)
				}
			}

			// Remove this thread from runnable.
			s.runnable[thr.index] = s.runnable[len(s.runnable)-1]
			s.runnable[thr.index].index = thr.index
			s.runnable = s.runnable[:len(s.runnable)-1]

			// If we're panicking, pass the error to Run
			// so it can shut down this execution.
			//
			// TODO: Capture the stack trace.
			if goErr != nil {
				s.goErr = goErr
				s.runThread.wake <- true
				return
			}

			// Otherwise, this is a regular thread exit.
			// Close our wake channel so Sched returns
			// immediately and release this goroutine.
			close(thr.wake)
			s.Sched()
		}()
		if debug {
			fmt.Printf("%v started\n", thr)
		}
		thr.desched()
		f()
	}()
	s.Sched()
}

// desched deschedules thread t until the scheduler selects it or all
// threads are aborted. In the case of a thread abort, it panics with
// threadAbort.
func (t *thread) desched() {
	if cont, ok := <-t.wake; ok && !cont {
		panic(threadAbort)
	}
}

var threadAbort = errors.New("thread aborted because of panic in another thread")

func (s *Scheduler) Sched() {
	this := s.curThread

	// Pick a thread to run next.
	if len(s.runnable) == 0 {
		// The last thread exited. Return to the Run thread.
		s.runThread.wake <- true
		return
	}
	s.curThread = s.runnable[s.as.Amb(len(s.runnable))]

	if debug {
		fmt.Printf("scheduling %v from %v\n", s.curThread, s.runnable)
	}

	// Switch to that thread.
	if this == s.curThread {
		return
	}
	s.curThread.wake <- true
	this.desched()
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
