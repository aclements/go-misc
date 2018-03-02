// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package split

import (
	"sync"
	"sync/atomic"
	"testing"
)

const (
	log2ValueLoggerBuf  = 8 // 256 entries per buffer
	log2ValueLoggerBufs = 1 // Double buffering

	valueLoggerIndexShift = 64 - (log2ValueLoggerBuf + log2ValueLoggerBufs)
	activeWriterBits      = 1 + log2ValueLoggerBuf // Room for max writers to a buffer, plus mark bit.
	bufMarkMask           = 1 << (activeWriterBits - 1)
)

type valueLoggerBuf [1 << log2ValueLoggerBuf]uint64

var valueLoggerBufPool = sync.Pool{New: func() interface{} { return new(valueLoggerBuf) }}

// valueLogger is a thread-safe logger for uint64 values.
type valueLogger struct {
	// control is the buffer control field. It consists of several
	// bit fields. The low bits consist of N fields that are each
	// activeWriterBits wide and corresponds to indexes into vals.
	// Field i counts the number of active writers to vals[i],
	// plus a bufMarkMask bit that indicates vals[i] is full.
	//
	// Bits valueLoggerIndexShift and up are an index into the
	// logical ring buffer formed by concatenating vals.
	//
	// TODO: Put this bit packing behind a type with methods?
	control uint64
	// vals is a double-buffered (though it could be more) ring
	// buffer for storing values. Using a pair of buffers allows
	// writes to proceed in one buffer while the other buffer is
	// being reallocated.
	vals [1 << log2ValueLoggerBufs]*valueLoggerBuf
	// allocLock protects allocating new buffers for vals. Access
	// to vals is already synchronized by control, but this offers
	// a convenient way to block writers waiting on a buffer to be
	// swapped out.
	allocLock sync.Mutex
}

func newValueLogger() valueLogger {
	var l valueLogger
	for i := range l.vals {
		l.vals[i] = valueLoggerBufPool.Get().(*valueLoggerBuf)
	}
	return l
}

func (l *valueLogger) append(v uint64) {
	// Claim a slot and increment the active count for that
	// buffer. The active count acts as a lock on vals[bufIdx].
	var i, bufIdx, activeShift uint64
	for {
		c := atomic.LoadUint64(&l.control)
		i = c >> valueLoggerIndexShift
		bufIdx = i / uint64(len(valueLoggerBuf{}))
		activeShift = bufIdx * activeWriterBits
		if (c>>activeShift)&bufMarkMask != 0 {
			// This buffer is still being swapped out.
			// Wait for it and retry.
			l.allocLock.Lock()
			l.allocLock.Unlock()
			continue
		}

		// Increment the index. This depends on uint64
		// wrap-around.
		newC := c + 1<<valueLoggerIndexShift
		// Increment the active writer count.
		newC += 1 << activeShift

		if atomic.CompareAndSwapUint64(&l.control, c, newC) {
			break
		}
	}

	// Put the value in the slot we claimed.
	l.vals[bufIdx][i%uint64(len(valueLoggerBuf{}))] = v

	// Decrement the active writer count for the buffer. If this
	// wrote to the last slot, set the buffer mark. If this is the
	// last writer to this buffer and the buffer is marked, this
	// writer is responsible for re-allocating the buffer.
	for {
		c := atomic.LoadUint64(&l.control)
		// Decrement the active writer count for this buffer.
		newC := c + (^uint64(0) << activeShift)
		// If this wrote to the last slot, set the buffer mark.
		if i%uint64(len(valueLoggerBuf{})) == uint64(len(valueLoggerBuf{})-1) {
			newC |= bufMarkMask << activeShift
		}
		if atomic.CompareAndSwapUint64(&l.control, c, newC) {
			// If this was the last writer to this buffer
			// and it's marked, this this writer is
			// responsible for re-allocating the buffer.
			if (newC>>activeShift)&(1<<activeWriterBits-1) != bufMarkMask {
				return
			}
			break
		}
	}

	// This writer is responsible for re-allocating the buffer.
	l.allocLock.Lock()
	completeBuf := l.vals[bufIdx]
	l.vals[bufIdx] = valueLoggerBufPool.Get().(*valueLoggerBuf)
	// Clear the buffer mark so writers can use this
	// buffer slot again. Too bad there's no AndUint64.
	for {
		c := atomic.LoadUint64(&l.control)
		newC := c &^ (bufMarkMask << activeShift)
		if atomic.CompareAndSwapUint64(&l.control, c, newC) {
			break
		}
	}
	l.allocLock.Unlock()
	l.process(completeBuf)
}

func (l *valueLogger) process(buf *valueLoggerBuf) {
	// In a real system, this would do something with the data in
	// buf. Here we just discard it.
	valueLoggerBufPool.Put(buf)
}

func BenchmarkLazyAggregationSplit(b *testing.B) {
	// Benchmark a lazy aggregating value logger.
	logger := New(func(l *valueLogger) { *l = newValueLogger() })

	b.RunParallel(func(pb *testing.PB) {
		for i := uint64(0); pb.Next(); i++ {
			logger.Get().(*valueLogger).append(i)
		}
	})
}

func BenchmarkLazyAggregationShared(b *testing.B) {
	// Non-sharded version of BenchmarkLazyAggregation.
	logger := newValueLogger()

	b.RunParallel(func(pb *testing.PB) {
		for i := uint64(0); pb.Next(); i++ {
			logger.append(i)
		}
	})
}
