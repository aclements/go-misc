// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

type WaitGroup struct {
	n       int
	waiters []*thread
}

func (g *WaitGroup) Add(delta int) {
	g.n += delta
	if g.n == 0 {
		waiters := g.waiters
		g.waiters = nil
		for _, t := range waiters {
			t.unblock()
		}
	}
}

func (g *WaitGroup) Done() {
	g.Add(-1)
}

func (g *WaitGroup) Wait() {
	if g.n == 0 {
		globalSched.Sched()
		return
	}
	this := globalSched.curThread
	g.waiters = append(g.waiters, this)
	this.block(g.reset)
}

func (g *WaitGroup) reset() {
	*g = WaitGroup{}
}
