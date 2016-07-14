// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package amb

import "fmt"

// StrategyDFS explores the ambiguous value space in depth-first order
// up to MaxDepth. It is deterministic and (given enough time) will
// explore the entire space.
type StrategyDFS struct {
	// MaxDepth specifies the maximum depth of the tree. If this
	// is 0, it defaults to DefaultMaxDepth.
	MaxDepth int

	branchWidths []int
	curPath      []int
	step         int
}

func (s *StrategyDFS) Reset() {
	s.branchWidths = nil
	s.curPath = nil
	s.step = 0
}

func (s *StrategyDFS) maxDepth() int {
	if s.MaxDepth == 0 {
		return DefaultMaxDepth
	}
	return s.MaxDepth
}

func (s *StrategyDFS) Amb(n int) (int, bool) {
	if s.step < len(s.curPath) {
		// We're in replay mode.
		if n != s.branchWidths[s.step] {
			panic(&ErrNondeterminism{fmt.Sprintf("Amb(%d) during replay, but previous call was Amb(%d)", n, s.branchWidths[s.step])})
		}
		res := s.curPath[s.step]
		s.step++
		return res, true
	}

	if len(s.curPath) == s.maxDepth() {
		return 0, false
	}

	// We're in exploration mode.
	s.branchWidths = append(s.branchWidths, n)
	s.curPath = append(s.curPath, 0)
	s.step++
	return 0, true
}

func (s *StrategyDFS) Next() bool {
	s.step = 0

	// Construct the next path prefix to explore.
	for i := len(s.curPath) - 1; i >= 0; i-- {
		s.curPath[i]++
		if s.curPath[i] < s.branchWidths[i] {
			break
		}
		s.curPath = s.curPath[:len(s.curPath)-1]
	}
	s.branchWidths = s.branchWidths[:len(s.curPath)]
	if len(s.branchWidths) == 0 {
		// We're out of paths.
		return false
	}
	return true
}

// ErrNondeterminism is the error used by deterministic strategies to
// indicate that the strategy detected that the application behaved
// non-deterministically.
type ErrNondeterminism struct {
	Detail string
}

func (e *ErrNondeterminism) Error() string {
	return "non-determinism detected: " + e.Detail
}
