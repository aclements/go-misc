// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// SCModel models all loads and stores as sequentially consistent.
// That is, there is a total order over all loads and stores. It
// implements sequential consistency using a direct operational
// semantics.
type SCModel struct{}

func (SCModel) String() string {
	return "SC"
}

func (SCModel) Eval(p *Prog, outcomes *OutcomeSet) {
	// Run the program in all possible ways, gather the results of
	// each load instruction, and at the end of each execution
	// record the outcome.
	outcomes.Reset(p)
	(&scGlobal{p, outcomes}).rec(scState{})
}

// scGlobal stores state that is global to an SC evaluation.
type scGlobal struct {
	p        *Prog
	outcomes *OutcomeSet
}

// scState stores the state of a program at a single point during
// execution.
type scState struct {
	mem     MemState
	pcs     [MaxThreads]int
	outcome Outcome
}

func (g *scGlobal) rec(s scState) {
	var opres int
	// Pick an op to execute next.
	any := false
	for tid := range g.p.Threads {
		op := g.p.Threads[tid].Ops[s.pcs[tid]]
		if op.Type != OpExit {
			any = true
			ns := s
			ns.mem, opres = op.Exec(ns.mem)
			if op.Type == OpLoad {
				ns.outcome |= Outcome(opres) << op.ID
			}
			ns.pcs[tid]++
			g.rec(ns)
		}
	}
	if !any {
		// This execution is done.
		g.outcomes.Add(s.outcome)
	}
}
