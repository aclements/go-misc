// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// rwmutex is a model of runtime.rwmutex.
package main

import (
	"github.com/aclements/go-misc/go-weave/amb"
	"github.com/aclements/go-misc/go-weave/weave"
)

//var sched = weave.Scheduler{Strategy: &amb.StrategyRandom{}}
var sched = weave.Scheduler{Strategy: &amb.StrategyDFS{}}

const verbose = false

func main() {
	sched.Run(func() {
		if verbose {
			print("start:")
		}
		var rw rwmutex
		for i := 0; i < 2; i++ {
			sched.Go(func() {
				rw.lock()
				rw.unlock()
			})
		}
		for i := 0; i < 2; i++ {
			sched.Go(func() {
				rw.rlock()
				rw.runlock()
			})
		}
	})
}

func atomicXadd(x *uint32, delta int32) uint32 {
	*x += uint32(delta)
	r := *x
	sched.Sched()
	return r
}

func atomicLoad(x *uint32) uint32 {
	r := *x
	sched.Sched()
	return r
}

func lock(m *weave.Mutex) {
	m.Lock()
}

func unlock(m *weave.Mutex) {
	m.Unlock()
}

type m struct {
	schedlink muintptr
	park      weave.Semaphore
}

type g struct {
	m *m
}

var curG = weave.NewTLS()

func notesleep(s *weave.Semaphore) {
	s.Acquire(1)
}

func notewakeup(s *weave.Semaphore) {
	s.Release(1)
}

func noteclear(s *weave.Semaphore) {
}

func getg() *g {
	gp, ok := curG.Get().(*g)
	if !ok {
		gp = &g{&m{}}
		curG.Set(gp)
	}
	return gp
}

type rwmutex struct {
	rLock      weave.Mutex // protects readers, readerPass, writer
	readers    muintptr    // list of pending readers
	readerPass uint32      // number of readers to skip readers list

	wLock  weave.Mutex // serializes writers
	writer muintptr    // pending writer waiting for completing readers

	readerCount uint32 // number of pending readers
	readerWait  uint32 // number of departing readers

	// Self-checking
	checkReaders uint32
	checkWriters uint32
}

type muintptr struct {
	mp *m
}

func (mp *muintptr) set(x *m) {
	mp.mp = x
}

func (mp *muintptr) ptr() *m {
	return mp.mp
}

func systemstack(x func()) {
	x()
}

func throw(x string) {
	panic(x)
}

const rwmutexMaxReaders = 1 << 30

// rlock locks rw for reading.
func (rw *rwmutex) rlock() {
	sched.Tracef("rw.readerCount (%d) += 1", rw.readerCount)
	if int32(atomicXadd(&rw.readerCount, 1)) < 0 {
		// A writer is pending. Park on the reader queue.
		sched.Trace("writer pending")
		systemstack(func() {
			lock(&rw.rLock)
			// Writer may have released while we were
			// getting the lock.
			sched.Trace("got rLock")
			if rw.readerPass > 0 {
				// Writer finished.
				rw.readerPass -= 1
				unlock(&rw.rLock)
			} else {
				// Queue this reader to be woken by
				// the writer.
				m := getg().m
				m.schedlink = rw.readers
				rw.readers.set(m)
				sched.Trace("reader queued")
				unlock(&rw.rLock)
				notesleep(&m.park)
				noteclear(&m.park)
			}
		})
	}

	// Self-check
	if rw.checkWriters != 0 {
		panic("rlock with writers")
	}
	rw.checkReaders++
}

// runlock undoes a single rlock call on rw.
func (rw *rwmutex) runlock() {
	if rw.checkReaders <= 0 {
		panic("runlock with no readers")
	}
	if rw.checkWriters != 0 {
		panic("runlock with writers")
	}
	rw.checkReaders--

	sched.Tracef("rw.readerCount (%d) -= 1", rw.readerCount)
	if r := int32(atomicXadd(&rw.readerCount, -1)); r < 0 {
		sched.Tracef("r = %d", r)
		if r+1 == 0 || r+1 == -rwmutexMaxReaders {
			throw("runlock of unlocked rwmutex")
		}
		// A writer is pending.
		sched.Tracef("rw.readerWait (%d) -= 1", rw.readerWait)
		if atomicXadd(&rw.readerWait, -1) == 0 {
			// The last reader unblocks the writer.
			sched.Trace("last reader")
			lock(&rw.rLock)
			w := rw.writer.ptr()
			if w != nil {
				sched.Trace("wake writer")
				notewakeup(&w.park)
			}
			unlock(&rw.rLock)
		}
	}
}

// lock locks rw for writing.
func (rw *rwmutex) lock() {
	// Resolve competition with other writers.
	lock(&rw.wLock)
	sched.Trace("got wLock")
	m := getg().m
	// Announce that there is a pending writer.
	sched.Tracef("rw.readerCount (%d) -= rwmutexMaxReaders", rw.readerCount)
	r := int32(atomicXadd(&rw.readerCount, -rwmutexMaxReaders)) + rwmutexMaxReaders
	// Wait for any active readers to complete.
	lock(&rw.rLock) // NEW
	if r != 0 {
		sched.Tracef("rw.readerWait (%d) += %d", rw.readerWait, r)
	}
	if r != 0 && atomicXadd(&rw.readerWait, r) != 0 {
		sched.Trace("waiting for readers")
		// Wait for reader to wake us up.
		systemstack(func() {
			rw.writer.set(m)
			unlock(&rw.rLock) // NEW
			notesleep(&m.park)
			noteclear(&m.park)
		})
	} else {
		sched.Trace("no readers")
		unlock(&rw.rLock) // NEW
	}

	// Self-check
	if rw.checkReaders != 0 {
		panic("lock with readers")
	}
	if rw.checkWriters != 0 {
		panic("lock with writers")
	}
	rw.checkWriters++
}

// unlock unlocks rw for writing.
func (rw *rwmutex) unlock() {
	// Self-check
	if rw.checkReaders != 0 {
		panic("unlock with readers")
	}
	if rw.checkWriters != 1 {
		panic("unlock with wrong writers")
	}
	rw.checkWriters--

	// Announce to readers that there is no active writer.
	sched.Tracef("rw.readerCount (%d) += rwmutexMaxReaders", rw.readerCount)
	r := int32(atomicXadd(&rw.readerCount, rwmutexMaxReaders))
	if r >= rwmutexMaxReaders {
		throw("unlock of unlocked rwmutex")
	}
	// Unblock blocked readers.
	lock(&rw.rLock)
	for rw.readers.ptr() != nil {
		sched.Tracef("wake reader")
		reader := rw.readers.ptr()
		rw.readers = reader.schedlink
		reader.schedlink.set(nil)
		notewakeup(&reader.park)
		r -= 1
	}
	// If r > 0, there are pending readers that aren't on the
	// queue. Tell them to skip waiting.
	rw.readerPass += uint32(r)
	unlock(&rw.rLock)
	// Allow other writers to proceed.
	sched.Tracef("release wLock")
	unlock(&rw.wLock)
}
