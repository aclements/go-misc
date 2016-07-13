// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

package main

import (
	"github.com/aclements/go-misc/go-weave/amb"
	"github.com/aclements/go-misc/go-weave/weave"
)

func mainOld() {
	weave.Run(func() {
		runnext, runqhead, runqtail = 0, 0, 0

		runqput(1)
		weave.Go(func() {
			runqput(2)
			runqget()
		})
		if runqempty() {
			panic("runqempty")
		}
	})
}

func main() {
	type state struct {
		runnext            int
		runqhead, runqtail int
	}
	weave.State = func() interface{} {
		return state{runnext, runqhead, runqtail}
	}
	states := 0
	weave.Run(func() {
		states++
		runnext, runqhead, runqtail = 0, 0, 0

		weave.Go(func() {
			for i := 0; i < 5; i++ {
				if amb.Amb(2) == 0 {
					runqget()
				} else {
					runqput(1)
				}
			}
		})
		var empty, nonempty, checks int
		weave.Monitor = func() {
			if runqhead == runqtail && runnext == 0 {
				empty++
			} else {
				nonempty++
			}
			checks++
		}
		v := runqempty()
		if empty == 0 && v == true {
			panic("spurious runqempty()")
		}
		if nonempty == checks && v == false {
			panic("spurious !runqempty()")
		}
	})
	println(states, "states")
}

var runnext int
var runqhead, runqtail int
var runq [256]int

func runqput(g int) {
	old := runnext
	runnext = g
	weave.Sched()
	if old == 0 {
		return
	}

	h := runqhead
	weave.Sched()
	t := runqtail
	weave.Sched()
	if t-h < len(runq) {
		runq[t%len(runq)] = g
		weave.Sched()
		runqtail = t + 1
		weave.Sched()
		return
	}
	panic("runq full")
}

func runqget() int {
	next := runnext
	if next != 0 {
		runnext = 0
		weave.Sched()
		return next
	}

	for {
		h := runqhead
		weave.Sched()
		t := runqtail
		weave.Sched()
		if t == h {
			return 0
		}
		g := runq[h%len(runq)]
		weave.Sched()
		if runqhead == h {
			runqhead = h + 1
			weave.Sched()
			return g
		}
	}
}

func runqemptyOld() bool {
	h := runqhead
	weave.Sched()
	t := runqtail
	weave.Sched()
	n := runnext
	weave.Sched()
	return h == t && n == 0
}

func runqempty() bool {
	for {
		h := runqhead
		weave.Sched()
		t := runqtail
		weave.Sched()
		n := runnext
		weave.Sched()
		t2 := runqtail
		weave.Sched()
		if t == t2 {
			return h == t && n == 0
		}
	}
}

func runqemptyTest() bool {
	for {
		h := runqhead
		weave.Sched()
		n := runnext
		weave.Sched()
		t := runqtail
		weave.Sched()
		return h == t && n == 0
	}
}
