// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

// Semaphore is a FIFO counted semaphore.
type Semaphore struct {
	avail   int
	wait    *semwait
	waitEnd *semwait
}

type semwait struct {
	n    int
	thr  *thread
	next *semwait
}

func (s *Semaphore) Acquire(n int) {
	if s.avail >= n {
		s.avail -= n
		return
	}
	this := globalSched.curThread
	w := &semwait{n, this, nil}
	if s.waitEnd != nil {
		s.waitEnd.next = w
	} else {
		s.wait = w
	}
	s.waitEnd = w
	this.block(s.reset)
}

func (s *Semaphore) Release(n int) {
	s.avail += n
	any := false
	for s.wait != nil && s.avail >= s.wait.n {
		any = true
		w := s.wait
		s.wait = w.next
		if s.wait == nil {
			s.waitEnd = nil
		}
		s.avail -= w.n
		w.thr.unblock()
	}
	if any {
		globalSched.Sched()
	}
}

func (s *Semaphore) reset() {
	*s = Semaphore{}
}
