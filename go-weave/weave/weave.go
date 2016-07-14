// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

import (
	"fmt"

	"github.com/aclements/go-misc/go-weave/amb"
)

type Scheduler struct {
	Strategy amb.Strategy

	as amb.Scheduler

	nextid               int
	threads              []*thread
	runThread, curThread *thread
	goErr                interface{}
}

type thread struct {
	id   int
	wake chan bool
}

func (t *thread) String() string {
	return fmt.Sprintf("t%d", t.id)
}

const debug = false

func (s *Scheduler) Run(main func()) {
	s.as = amb.Scheduler{Strategy: s.Strategy}

	s.as.Run(func() {
		// Initialize state.
		s.nextid = 0
		s.threads = nil
		s.runThread = &thread{-1, make(chan bool)}
		s.curThread = s.runThread
		s.goErr = nil
		s.Go(main)
		if s.goErr != nil {
			// TODO: Abort all threads.
			panic(s.goErr)
		}
		if debug {
			fmt.Println("run done")
		}
	})
}

func (s *Scheduler) Go(f func()) {
	thr := &thread{s.nextid, make(chan bool)}
	s.nextid++
	s.threads = append(s.threads, thr)
	go func() {
		defer func() {
			goErr := recover()
			if goErr != nil {
				// Return to the Run thread.
				s.goErr = goErr
				if debug {
					fmt.Printf("%v panicked: %v\n", thr, goErr)
				}
				s.runThread.wake <- true
				return
			}
			// Remove this thread.
			if debug {
				fmt.Printf("%v exited\n", thr)
			}
			for i, thr2 := range s.threads {
				if thr == thr2 {
					copy(s.threads[i:], s.threads[i+1:])
					s.threads = s.threads[:len(s.threads)-1]
					close(thr.wake)
					s.Sched()
					return
				}
			}
			panic("thread not found in threads")
		}()
		if debug {
			fmt.Printf("%v started\n", thr)
		}
		<-thr.wake
		f()
	}()
	s.Sched()
}

func (s *Scheduler) Sched() {
	this := s.curThread

	// Pick a thread to run next.
	if len(s.threads) == 0 {
		// The last thread exited. Return to the Run thread.
		s.runThread.wake <- true
		return
	}
	s.curThread = s.threads[s.as.Amb(len(s.threads))]

	if debug {
		fmt.Printf("scheduling %v from %v\n", s.curThread, s.threads)
	}

	// Switch to that thread.
	if this == s.curThread {
		return
	}
	s.curThread.wake <- true
	<-this.wake
}

func (s *Scheduler) Amb(n int) int {
	return s.as.Amb(n)
}
