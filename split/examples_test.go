// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package split

import (
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func Example_counter() {
	// This example demonstrates concurrent updates to a split
	// counter. The counter can be updated using an atomic
	// operation. The final result is the sum of the shard values.
	counter := New(func(*uint64) {})

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			atomic.AddUint64(counter.Get().(*uint64), 1)
			wg.Done()
		}()
	}
	wg.Wait()

	// Sum up the counter. In this example, the Range isn't
	// running concurrently with the updates above, but if it
	// were, the sum would be approximate. Specifically, any
	// updates that happened between the call to Range and when it
	// returns may or may not be included in the sum depending on
	// exact timing. For most counters, this is acceptable because
	// updates to the counter are already unordered.
	var sum uint64
	counter.Range(func(np *uint64) {
		sum += atomic.LoadUint64(np)
	})

	// Output: 64
	fmt.Println(sum)
}

func Example_logging() {
	// This example demonstrates concurrent appends to a split
	// log. Each shard of the log is protected by a mutex. The log
	// is combined by sorting the log records in timestamp order.
	type record struct {
		when time.Time
		msg  string
	}
	type shard struct {
		sync.Mutex
		log []record
	}
	logger := New(func(*shard) {})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			for j := 0; j < 4; j++ {
				msg := fmt.Sprintf("goroutine %d message %d", i, j)
				rec := record{time.Now(), msg}
				shard := logger.Get().(*shard)
				shard.Lock()
				shard.log = append(shard.log, rec)
				shard.Unlock()
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	// Collect and sort the log records. This isn't running
	// concurrently with log appends, but for demonstration
	// purposes it's written so it could. To get a consistent view
	// of the log, this uses two-phase locking: it makes two
	// passes through the shards where the first pass locks all of
	// the shards and collects the records, and the second pass
	// unlocks all of the shards.
	var combined []record
	logger.Range(func(val *shard) {
		val.Lock()
		combined = append(combined, val.log...)
	}, func(val *shard) {
		val.Unlock()
	})
	sort.Slice(combined, func(i, j int) bool { return combined[i].when.Before(combined[j].when) })

	// Unordered output:
	// goroutine 3 message 0
	// goroutine 3 message 1
	// goroutine 3 message 2
	// goroutine 3 message 3
	// goroutine 2 message 0
	// goroutine 1 message 0
	// goroutine 1 message 1
	// goroutine 1 message 2
	// goroutine 1 message 3
	// goroutine 2 message 1
	// goroutine 0 message 0
	// goroutine 2 message 2
	// goroutine 2 message 3
	// goroutine 0 message 1
	// goroutine 0 message 2
	// goroutine 0 message 3
	for _, rec := range combined {
		fmt.Println(rec.msg)
	}
}

func Example_randomSource() {
	// This example demonstrates concurrent random number
	// generation using split random number generators.
	var seed int64
	type lockedRand struct {
		sync.Mutex
		*rand.Rand
	}
	randSource := New(func(r *lockedRand) {
		r.Rand = rand.New(rand.NewSource(atomic.AddInt64(&seed, 1)))
	})

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			for j := 0; j < 64; j++ {
				// Generate a random number using a
				// local random source. rand.Rand
				// isn't thread-safe, so we lock it.
				r := randSource.Get().(*lockedRand)
				r.Lock()
				fmt.Println(r.Int())
				r.Unlock()
			}
			wg.Done()
		}()
	}
	wg.Wait()
}

func Example_optimisticSnapshot() {
	// This example demonstrates computing an instant-in-time
	// consistent snapshot of a sharded value without blocking
	// writers. In particular, writers in this example can update
	// multiple shards transactionally, so this requires careful
	// synchronization between readers and writers to get a
	// sequentially consistent view of the entire sharded value.
	//
	// The writer moves "units" between shards: initially all
	// shards have a count of 0 and the writer repeatedly picks
	// two shards and transactionally decrements the value of one
	// shard and increments the value of the other. Hence, at any
	// instant, the shards should all sum to 0.
	//
	// Since the Range callback doesn't see all shards at the same
	// instant, it can't simply add up the values of the shards.
	// If it did, the following could happen:
	//
	// 1. Suppose there are two shards with counts {0, 0}
	//
	// 2. Goroutine 1 calls Range. The callback reads shard 1,
	// which is 0, and adds 0 to the sum.
	//
	// 3. On goroutine 2, a writer atomically moves a unit from
	// shard 1 to shard 2, so now the shard values are {-1, 1}.
	//
	// 4. On goroutine 1, the Range continues and the callback
	// reads shard 2, which has value 1, and adds 1 to the sum.
	//
	// Now the value of the sum is 1, even though at any given
	// instant all of the shards added up to 0.
	//
	// This examples solves this using a sequence number in each
	// shard that is updated on every change to that shard. The
	// reader reads all of the shards repeatedly until it gets two
	// complete reads in a row where the sequence numbers didn't
	// change. This means no modifications raced with the read, so
	// it observed a consistent snapshot.

	type shard struct {
		val int64  // The unit count of this shard.
		seq uint64 // Sequence number; the low bit indicates this shard is unstable.
	}
	val := New(func(*shard) {})

	// Start a set of goroutines that simply pick shards to move
	// units between. These don't move the units.
	shards := make(chan *shard, 64)
	var wg sync.WaitGroup
	var stop uint32
	for i := 0; i < cap(shards); i++ {
		wg.Add(1)
		go func() {
			for atomic.LoadUint32(&stop) == 0 {
				// Pick a shard and send it to the churner.
				shard := val.Get().(*shard)
				shards <- shard
				// We're likely to get the same shard
				// again if we just loop around.
				// Perturb the scheduler.
				runtime.Gosched()
			}
			wg.Done()
		}()
	}

	// Start a goroutine that moves units between the shards
	// picked by the above goroutines.
	go func() {
		for {
			// Pick a pair of shards.
			shard1, ok := <-shards
			if !ok {
				break
			}
			shard2, ok := <-shards
			if !ok {
				break
			}
			if shard1 == shard2 {
				continue
			}

			// Increment the sequence number on both
			// shards to indicate their values are
			// unstable.
			atomic.AddUint64(&shard1.seq, 1)
			atomic.AddUint64(&shard2.seq, 1)

			// Move a unit from shard1 to shard2.
			atomic.AddInt64(&shard1.val, -1)
			atomic.AddInt64(&shard2.val, +1)

			// Increment the sequence number again to
			// indicate the shards changed, but are now
			// stable.
			atomic.AddUint64(&shard1.seq, 1)
			atomic.AddUint64(&shard2.seq, 1)
		}
	}()
	time.Sleep(time.Millisecond)

	// Retrieve a consistent sum of the shards. The sum should be
	// zero. This uses optimistic concurrency control and does not
	// block the writer, so it may have to read the shards
	// multiple times until it gets two reads in a row where none
	// of the sequence numbers have changed.
	var valSum int64
	var prevSeqSum uint64
	for {
		valSum = 0
		var seqSum uint64
		val.Range(func(s *shard) {
			// Within just this shard, we also need to
			// perform a consistent read of its value and
			// sequence number. If we could read both
			// fields in a single atomic operation, this
			// wouldn't be necessary, but since we can't,
			// we also use optimistic concurrency within
			// the shard.
			for {
				// Wait until the sequence number is
				// even, indicating that the sequence
				// number and value are stable.
				var seq1 uint64
				for {
					if seq1 = atomic.LoadUint64(&s.seq); seq1&1 == 0 {
						break
					}
					runtime.Gosched()
				}
				// Read the value optimistically.
				val := atomic.LoadInt64(&s.val)
				// Re-read the sequence number. If it
				// hasn't changed, then we know we got
				// a consistent read of both the value
				// and the sequence number. Otherwise,
				// try again.
				if atomic.LoadUint64(&s.seq) == seq1 {
					// Got a consistent read.
					// Update the value sum and
					// the sequence number
					// snapshot.
					valSum += val
					seqSum += seq1
					break
				}
			}
		})
		if seqSum == prevSeqSum {
			// We got two reads of the shards in a row
			// with the same sequence numbers. That means
			// no updates happened between those reads, so
			// the values we observed were consistent.
			break
		}
		prevSeqSum = seqSum
	}

	// Exit all workers.
	atomic.StoreUint32(&stop, 1)
	wg.Wait()
	close(shards)

	// Output: Values sum to 0
	fmt.Printf("Values sum to %d\n", valSum)
}

// TODO: SRCU-style grace period algorithm? Consistent counter using
// two epochs (though I'm not sure what it could be tracking that
// would require a sequentially consistent snapshot)?
