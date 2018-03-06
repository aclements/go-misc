// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package split

import (
	"sync"
	"sync/atomic"
)

// RWMutex is a scalable reader/writer mutual exclusion lock. The lock
// can be held by an arbitrary number of readers or a single writer.
// The zero value for a RWMutex is an unlocked mutex.
//
// In contrast with sync.RWMutex, this lock attempts to scale to any
// number of cores simultaneously acquiring read locks. However, this
// makes obtaining the lock in write mode more expensive.
type RWMutex struct {
	readLocks *Value
	writeLock sync.Mutex
	initLock  sync.Mutex
	init      uint32
}

// doInit performs lazily initialization on the first use of m.
func (m *RWMutex) doInit() {
	// Acquire the initialization lock to protect against
	// concurrent initialization.
	m.initLock.Lock()
	defer m.initLock.Unlock()
	if atomic.LoadUint32(&m.init) != 0 {
		// Another goroutine initialized the mutex while we
		// were waiting on the shard lock.
		return
	}
	m.readLocks = New(func(*sync.Mutex) {
		// Block creating new shards while the write lock is
		// held.
		m.writeLock.Lock()
		m.writeLock.Unlock()
	})
	atomic.StoreUint32(&m.init, 1)
}

// Lock acquires m in writer mode. This blocks all readers and
// writers.
func (m *RWMutex) Lock() {
	if atomic.LoadUint32(&m.init) == 0 {
		m.doInit()
	}
	// Block other writers and creation of new shards.
	m.writeLock.Lock()
	// Acquire all read locks.
	m.readLocks.Range(func(s *sync.Mutex) {
		s.Lock()
	})
}

// Unlock releases m from writer mode. The mutex must currently be
// held in writer mode.
func (m *RWMutex) Unlock() {
	m.readLocks.Range(func(s *sync.Mutex) {
		s.Unlock()
	})
	m.writeLock.Unlock()
}

// RWMutexRUnlocker is a token used to unlock an RWMutex in read mode.
type RWMutexRUnlocker struct {
	shard *sync.Mutex
}

// RLock acquires m in read mode. This blocks other goroutines from
// acquiring it in write mode, but does not generally block them from
// acquiring it in read mode. The caller must used the returned
// RWMutexRUnlocker to release the lock.
func (m *RWMutex) RLock() RWMutexRUnlocker {
	if atomic.LoadUint32(&m.init) == 0 {
		m.doInit()
	}
	shard := m.readLocks.Get().(*sync.Mutex)
	shard.Lock()
	return RWMutexRUnlocker{shard}
}

// RUnlock releases an RWMutex from read mode.
func (c RWMutexRUnlocker) RUnlock() {
	c.shard.Unlock()
}

func Example_rwMutex() {
	var m RWMutex

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			m.RLock().RUnlock()
			wg.Done()
		}()
	}
	wg.Wait()
}
