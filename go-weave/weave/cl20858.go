// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

package main

import (
	"github.com/aclements/go-misc/go-weave/amb"
	"github.com/aclements/go-misc/go-weave/weave"
)

var sched = weave.Scheduler{Strategy: &amb.StrategyDFS{}}

func mainOld() {
	sched.Run(func() {
		runnext, runqhead, runqtail = 0, 0, 0

		runqput(1)
		sched.Go(func() {
			runqput(2)
			runqget()
		})
		if runqempty() {
			panic("runqempty")
		}
	})
}

func main() {
	states := 0
	sched.Run(func() {
		states++
		runnext, runqhead, runqtail = 0, 0, 0

		sched.Go(func() {
			for i := 0; i < 5; i++ {
				if sched.Amb(2) == 0 {
					runqget()
				} else {
					runqput(1)
				}
			}
		})
		var empty, nonempty, checks int
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
	sched.Sched()
	if old == 0 {
		return
	}

	h := runqhead
	sched.Sched()
	t := runqtail
	sched.Sched()
	if t-h < len(runq) {
		runq[t%len(runq)] = g
		sched.Sched()
		runqtail = t + 1
		sched.Sched()
		return
	}
	panic("runq full")
}

func runqget() int {
	next := runnext
	if next != 0 {
		runnext = 0
		sched.Sched()
		return next
	}

	for {
		h := runqhead
		sched.Sched()
		t := runqtail
		sched.Sched()
		if t == h {
			return 0
		}
		g := runq[h%len(runq)]
		sched.Sched()
		if runqhead == h {
			runqhead = h + 1
			sched.Sched()
			return g
		}
	}
}

func runqemptyOld() bool {
	h := runqhead
	sched.Sched()
	t := runqtail
	sched.Sched()
	n := runnext
	sched.Sched()
	return h == t && n == 0
}

func runqempty() bool {
	for {
		h := runqhead
		sched.Sched()
		t := runqtail
		sched.Sched()
		n := runnext
		sched.Sched()
		t2 := runqtail
		sched.Sched()
		if t == t2 {
			return h == t && n == 0
		}
	}
}

func runqemptyTest() bool {
	for {
		h := runqhead
		sched.Sched()
		n := runnext
		sched.Sched()
		t := runqtail
		sched.Sched()
		return h == t && n == 0
	}
}
