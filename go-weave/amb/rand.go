// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package amb

import "math/rand"

// StrategyRandom explores the ambiguous value space randomly. It
// makes no attempt to avoid repeatedly visiting the same point, nor
// does it know when it has explored the entire space.
type StrategyRandom struct {
	// MaxDepth specifies the maximum depth of the tree. If this
	// is 0, it defaults to DefaultMaxDepth.
	MaxDepth int

	// MaxPaths specifies the maximum number of paths to explore.
	// If this is 0, the number of paths is unbounded.
	MaxPaths int

	step, paths int
}

func (s *StrategyRandom) Reset() {
	s.step = 0
	s.paths = 0
}

func (s *StrategyRandom) maxDepth() int {
	if s.MaxDepth == 0 {
		return DefaultMaxDepth
	}
	return s.MaxDepth
}

func (s *StrategyRandom) Amb(n int) (int, bool) {
	if s.step == s.maxDepth() {
		return 0, false
	}
	s.step++
	return rand.Intn(n), true
}

func (s *StrategyRandom) Next() bool {
	s.step = 0
	s.paths++
	return s.MaxPaths == 0 || s.paths < s.MaxPaths
}
