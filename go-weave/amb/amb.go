// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// TODO: Rather than having a global interface and not having a good
// way to switch between different amb strategies, make amb an object
// satisfying the interface { Amb(n int); Next() bool }. You can then
// have a generic driver that takes an amb implementation and a root
// function and runs it (and maybe handles state checking). Weave
// could also implement different strategies, which could be
// orthogonal: e.g., PCT could be used with either a randomized amb
// strategy or a precise one.

package amb

import "fmt"

var branchWidths []int
var curPath []int
var step int

// MaxDepth is the maximum depth to explore to.
var MaxDepth = 100

func Run(root func()) {
	if curPath != nil {
		panic("nested Run call")
	}

	branchWidths = []int{}
	curPath = []int{}

	for {
		// Run to a leaf.
		step = 0
		run1(root)

		// Find the next path to explore.
		for i := len(curPath) - 1; i >= 0; i-- {
			curPath[i]++
			if curPath[i] < branchWidths[i] {
				break
			}
			curPath = curPath[:len(curPath)-1]
		}
		branchWidths = branchWidths[:len(curPath)]
		if len(branchWidths) == 0 {
			// We're out of paths.
			break
		}
	}

	branchWidths, curPath = nil, nil
}

func run1(root func()) {
	defer func() {
		err := recover()
		if err != nil {
			// TODO: Report path.
			fmt.Println("failure:", err)
		}
	}()
	root()
}

func Amb(n int) int {
	if step < len(curPath) {
		// We're in replay mode.
		if n != branchWidths[step] {
			panic(fmt.Sprintf("non-determinism detected: Amb(%d) during replay, but previous call was Amb(%d)", n, branchWidths[step]))
		}
		res := curPath[step]
		step++
		return res
	}

	if len(curPath) == MaxDepth {
		panic("max Amb depth exceeded")
	}

	// We're in exploration mode.
	branchWidths = append(branchWidths, n)
	curPath = append(curPath, 0)
	step++
	return 0
}

func IsReplay() bool {
	return step < len(curPath)
}
