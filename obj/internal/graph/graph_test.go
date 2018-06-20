// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

// Example graph from Muchnick, "Advanced Compiler Design &
// Implementation", figure 8.21.
var graphMuchnick = MakeBiGraph(IntGraph{
	0: {1},
	1: {2},
	2: {3, 4},
	3: {2},
	4: {5, 6},
	5: {7},
	6: {7},
	7: {},
})

// Example graph from
// https://www.seas.harvard.edu/courses/cs252/2011sp/slides/Lec04-SSA.pdf
// slide 24.
var graphCS252 = MakeBiGraph(IntGraph{
	0: {1},
	1: {2, 5},
	2: {3, 4},
	3: {6},
	4: {6},
	5: {1, 7},
	6: {7},
	7: {8},
	8: {},
})
