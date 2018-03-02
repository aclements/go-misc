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
	fmt.Println(sum)

	// Output: 64
}

func Example_counterConsistent() {
	// This example is similar to the "Counter" example, but the
	// counter goes both up and down. Specifically, each goroutine
	// increments the counter and then decrements the counter, but
	// the increment and decrement may happen on different shards.
	// The sum of the counter at any instant is between 0 and the
	// number of goroutines, but since Range can't see all of the
	// shards at the same instant, it may observe a decrement
	// without an increment, leading to a negative sum.
	//
	// In this example, we solve this problem using two-phase
	// locking.
	type shard struct {
		val uint64
		sync.Mutex
	}
	counter := New(func(*shard) {})

	const N = 64
	var wg sync.WaitGroup
	var stop uint32
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			for atomic.LoadUint32(&stop) == 0 {
				s := counter.Get().(*shard)
				s.Lock()
				s.val++
				s.Unlock()

				// .. do some work, maybe get moved to
				// a different shard ..
				runtime.Gosched()

				s = counter.Get().(*shard)
				s.Lock()
				s.val--
				s.Unlock()
			}
			wg.Done()
		}()
	}

	// Let the goroutines do some work.
	time.Sleep(time.Millisecond)

	// Capture a consistent sum by locking all of the shards, then
	// unlocking all of them. This must be done in a single Range
	// call to prevent the number of shards from changing.
	var sum uint64
	counter.Range(func(s *shard) {
		s.Lock()
		sum += s.val
	}, func(s *shard) {
		s.Unlock()
	})

	// Stop the writers.
	atomic.StoreUint32(&stop, 1)
	wg.Wait()

	if sum < 0 || sum > N {
		fmt.Println("bad sum:", sum)
	}
	// Output:
}

func Example_logging() {
	// This example demonstrates concurrent appends to a split
	// log. Each shard of the log is protected by a mutex. The log
	// is combined by sorting the log records in timestamp order.
	// This example collects a consistent snapshot of the log
	// using these timestamps.
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
				shard := logger.Get().(*shard)
				shard.Lock()
				// We have to record the time under
				// the lock to ensure it's ordered for
				// the reader.
				rec := record{time.Now(), msg}
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
	// of the log, this uses timestamp ordering: it records the
	// current time before calling Range and ignores any records
	// from after that time. For logs it makes sense to get a
	// consistent snapshot: a given worker could move between
	// shards and it would be confusing to see later log records
	// from that worker without earlier log records.
	snapshot := time.Now()
	var combined []record
	logger.Range(func(val *shard) {
		val.Lock()
		log := val.log
		val.Unlock()
		// Trim records after time "snapshot".
		i := sort.Search(len(log), func(i int) bool {
			return log[i].when.After(snapshot)
		})
		log = log[:i]
		combined = append(combined, log...)
	})
	sort.Slice(combined, func(i, j int) bool { return combined[i].when.Before(combined[j].when) })

	for _, rec := range combined {
		fmt.Println(rec.msg)
	}

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

func Example_optimisticTransactions() {
	// This example demonstrates computing an instant-in-time
	// consistent snapshot of a sharded value without blocking
	// writers. Writers in this example can update multiple shards
	// transactionally, so this requires careful synchronization
	// between readers and writers to get a sequentially
	// consistent view of the entire sharded value.
	//
	// Each transaction moves a "unit" between two shards.
	// Initially all shards have a count of 0. Each writer
	// repeatedly picks two shards and transactionally decrements
	// the value of one shard and increments the value of the
	// other. Hence, at any instant, the shards should all sum to
	// 0.
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
	// 3. On goroutine 2, a writer transactionally moves a unit
	// from shard 1 to shard 2, so now the shard values are {-1,
	// 1}.
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
		order uint32 // Lock order of the shards.
		val   int64  // The unit count of this shard.
		seq   uint64 // Sequence number; the low bit indicates this shard is unstable.
	}
	var lockOrder uint32
	val := New(func(s *shard) {
		s.order = atomic.AddUint32(&lockOrder, 1) - 1
	})

	acquireSeq := func(p *uint64) {
		// "Acquire" a sequence counter by spinning until the
		// counter is even and then incrementing it.
		for {
			v := atomic.LoadUint64(p)
			if v&1 == 0 && atomic.CompareAndSwapUint64(p, v, v+1) {
				return
			}
			runtime.Gosched()
		}
	}

	// Start a set of writer goroutines.
	var wg sync.WaitGroup
	var stop uint32
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			for atomic.LoadUint32(&stop) == 0 {
				// Pick a first shard.
				shard1 := val.Get().(*shard)
				// Try to get moved to a different shard.
				runtime.Gosched()
				// Pick a second shard.
				shard2 := val.Get().(*shard)
				if shard1 == shard2 {
					continue
				}

				// Put the shards in lock order.
				lock1, lock2 := shard1, shard2
				if lock1.order > lock2.order {
					lock1, lock2 = lock2, lock1
				}

				// Lock both shards. Odd sequence
				// numbers also indicates their values
				// are unstable.
				acquireSeq(&lock1.seq)
				acquireSeq(&lock2.seq)

				// Move a unit from shard1 to shard2.
				atomic.AddInt64(&shard1.val, -1)
				atomic.AddInt64(&shard2.val, +1)

				// Increment the sequence numbers
				// again to indicate the shards
				// changed, but are now stable.
				atomic.AddUint64(&lock1.seq, 1)
				atomic.AddUint64(&lock2.seq, 1)
			}
			wg.Done()
		}()
	}

	// Let the writers get going.
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

	fmt.Printf("Values sum to %d\n", valSum)
	// Output: Values sum to 0
}

// TODO: SRCU-style grace period algorithm? Consistent counter using
// two epochs (though I'm not sure what it could be tracking that
// would require a sequentially consistent snapshot)?
