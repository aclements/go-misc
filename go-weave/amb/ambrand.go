// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package amb

import (
	"fmt"
	"math/rand"
)

var step int

// MaxDepth is the maximum depth to explore to.
var MaxDepth = 100

func Run(root func()) {
	startProgress()
	for {
		step = 0
		run1(root)
		count++
	}
	stopProgress()
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
	if step == MaxDepth {
		panic("max Amb depth exceeded")
	}
	step++
	return rand.Intn(n)
}

func IsReplay() bool {
	return false
}
