// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

import "fmt"

type Mutex struct {
	locked  bool
	waiters []*thread
}

func (m *Mutex) Lock() {
	if !m.locked {
		m.locked = true
		return
	}
	this := globalSched.curThread
	m.waiters = append(m.waiters, this)
	this.block(m.reset)
}

func (m *Mutex) Unlock() {
	if !m.locked {
		panic("attempt to Unlock unlocked Mutex")
	}
	if len(m.waiters) == 0 {
		m.locked = false
	} else {
		// Pick an arbitrary thread to wake up.
		next := globalSched.Amb(len(m.waiters))
		t := m.waiters[next]
		m.waiters[next] = m.waiters[len(m.waiters)-1]
		m.waiters = m.waiters[:len(m.waiters)-1]
		t.unblock()
	}
	globalSched.Sched()
}

func (m *Mutex) reset() {
	*m = Mutex{}
}

type RWMutex struct {
	r, w             int
	readers, writers []*thread
}

func (rw *RWMutex) Lock() {
	if rw.r == 0 && rw.w == 0 {
		rw.w++
		return
	}
	this := globalSched.curThread
	rw.writers = append(rw.writers, this)
	this.block(rw.reset)
}

func (rw *RWMutex) RLock() {
	if rw.w == 0 {
		rw.r++
		return
	}
	this := globalSched.curThread
	rw.readers = append(rw.readers, this)
	this.block(rw.reset)
}

func (rw *RWMutex) reset() {
	*rw = RWMutex{}
}

func (rw *RWMutex) Unlock() {
	rw.w--
	rw.release()
}

func (rw *RWMutex) RUnlock() {
	rw.r--
	rw.release()
}

func (rw *RWMutex) release() {
	if rw.w != 0 {
		panic(fmt.Sprintf("bad RWMutex writer count: %d", rw.w))
	}
	if len(rw.readers) > 0 {
		// Wake all readers.
		rw.r += len(rw.readers)
		for _, t := range rw.readers {
			t.unblock()
		}
		rw.readers = rw.readers[:0]
	} else if rw.r == 0 && len(rw.writers) > 0 {
		// Wake one writer.
		rw.w++
		next := globalSched.Amb(len(rw.writers))
		t := rw.writers[next]
		rw.writers[next] = rw.writers[len(rw.writers)-1]
		rw.writers = rw.writers[:len(rw.writers)-1]
		t.unblock()
	}
	globalSched.Sched()
}
