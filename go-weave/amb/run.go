// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package amb

import (
	"errors"
	"fmt"
)

// A Strategy describes how to explore a space of ambiguous values.
// Such a space can be viewed as a tree, where a call to Amb
// introduces a node with fan-out n and a call to Next terminates a
// path.
type Strategy interface {
	// Amb returns an "ambiguous" value in the range [0, n). If
	// the current path cannot be continued (for example, it's
	// reached a maximum depth), it returns 0, false.
	//
	// The first call to Amb after constructing a Strategy or
	// calling Next always starts at the root of the tree.
	//
	// Amb may panic with ErrNondeterminism if it detects that the
	// application is behaving non-deterministically (for example,
	// when replaying a previously explored path, the value of n
	// is different from when Amb was called during a previous
	// exploration of this path). This is best-effort and some
	// strategies may not be able to detect this.
	Amb(n int) (int, bool)

	// Next terminates the current path. If there are no more
	// paths to explore, Next returns false. A Strategy is not
	// required to ever return false (for example, a randomized
	// strategy may not know that it's explored the entire space).
	Next() bool

	// Reset resets the state of this Strategy to the point where
	// no paths have been explored.
	Reset()
}

// DefaultMaxDepth is the default maximum tree depth if it is
// unspecified.
var DefaultMaxDepth = 100

// Scheduler uses a Strategy to execute a function repeatedly at
// different points in a space of ambiguous values.
type Scheduler struct {
	// Strategy specifies the strategy for exploring the execution
	// space.
	Strategy Strategy

	active bool
}

var curStrategy Strategy
var curPanic interface{}

// Run calls root repeatedly at different points in the ambiguous
// value space.
func (s *Scheduler) Run(root func()) {
	if s.active {
		panic("nested Run call")
	}
	s.active = true
	defer func() { s.active = false }()

	count = 0
	startProgress()
	defer stopProgress()

	s.Strategy.Reset()
	for {
		s.run1(root)

		if !s.Strategy.Next() {
			break
		}
		count++
	}
}

func (s *Scheduler) run1(root func()) {
	defer func() {
		err := recover()
		if err != nil {
			// TODO: Report path.
			fmt.Println("failure:", err)
		}
	}()
	root()
}

// Amb returns a value in the range [0, n).
//
// Amb may panic with PathTerminated to indicate an execution path is
// being forcibly terminated by the Strategy. If Amb is called on a
// goroutine other than the goroutine that called Run, the goroutine
// is responsible for recovering PathTerminated panics and forwarding
// the panic to the goroutine that called Run.
func (s *Scheduler) Amb(n int) int {
	x, ok := s.Strategy.Amb(n)
	if !ok {
		panic(PathTerminated)
	}
	return x
}

// PathTerminated is panicked by Scheduler.Amb to indicate that Run
// should continue to the next path.
var PathTerminated = errors.New("path terminated")
