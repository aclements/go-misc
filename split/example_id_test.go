// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package split

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// UIDGenerator generates unique, reasonably dense integer IDs.
//
// The implementation supports efficient concurrent generation of IDs.
// It works by retrieving batches of 256 IDs at a time from a central
// ID source, and sub-allocating IDs within those batches.
type UIDGenerator struct {
	v        *Value
	base     uint64
	baseLock sync.Mutex
}

const batchSize = 256

type uidShard struct {
	next, limit uint64
}

// NewUIDGenerator returns a new generator for unique IDs.
func NewUIDGenerator() *UIDGenerator {
	g := &UIDGenerator{}
	g.v = New(func(s *uidShard) {
		g.baseLock.Lock()
		defer g.baseLock.Unlock()
		*s = uidShard{g.base, g.base + batchSize}
		g.base += batchSize
	})
	return g
}

// GenUID returns a uint64 that is distinct from the uint64 returned
// by every other call to GenUID on g.
func (g *UIDGenerator) GenUID() uint64 {
retry:
	shard := g.v.Get().(*uidShard)
	limit := atomic.LoadUint64(&shard.limit)
	id := atomic.AddUint64(&shard.next, 1)
	if id < limit {
		// Fast path: we got an ID in the batch.
		return id
	}
	// Slow path: the batch ran out. Get a new batch. This
	// is tricky because multiple genUIDs could enter the
	// slow path for the same shard.
	g.baseLock.Lock()
	// Check if another genUID already got a new batch for
	// this shard.
	if atomic.LoadUint64(&shard.limit) != limit {
		g.baseLock.Unlock()
		goto retry
	}
	// This genUID won the race to get a new shard for
	// this batch.
	base := g.base
	g.base += batchSize
	// Store the next first so another genUID on this
	// shard will continue to fail the limit check.
	atomic.StoreUint64(&shard.next, base+1)
	// Now store to limit, which commits this batch.
	atomic.StoreUint64(&shard.limit, base+batchSize)
	g.baseLock.Unlock()
	return base
}

func Example_idGenerator() {
	ids := NewUIDGenerator()

	// Generate a bunch of UIDs in parallel.
	const nGoroutines = 64
	const nIDs = 500
	generatedIDs := make([]uint64, nGoroutines*nIDs)
	var wg sync.WaitGroup
	for i := 0; i < nGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			// Generate 500 unique IDs.
			for j := 0; j < nIDs; j++ {
				generatedIDs[i*nIDs+j] = ids.GenUID()
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	// Check that all UIDs were distinct.
	idMap := make(map[uint64]bool)
	for _, id := range generatedIDs {
		if idMap[id] {
			fmt.Printf("ID %d generated more than once\n", id)
			return
		}
		idMap[id] = true
	}
	// Output: All IDs were unique
	fmt.Println("All IDs were unique")
}
