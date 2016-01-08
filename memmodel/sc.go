// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// SCModel models all loads and stores as sequentially consistent.
// That is, there is a total order over all loads and stores.
type SCModel struct{}

func (SCModel) String() string {
	return "SC"
}

type SCState struct {
	// Program state.
	mem     MemState
	pcs     [MaxThreads]int
	outcome Outcome
}

func (SCModel) Eval(p *Prog, outcomes *OutcomeSet) {
	// Run the program in all possible ways, gather the results of
	// each load instruction, and at the end of each execution
	// record the outcome.
	outcomes.Reset(p)
	scRec(p, outcomes, SCState{})
}

func scRec(p *Prog, outcomes *OutcomeSet, s SCState) {
	var opres int
	// Pick an op to execute next.
	any := false
	for tid := range p.Threads {
		op := p.Threads[tid].Ops[s.pcs[tid]]
		if op.Type != OpExit {
			any = true
			ns := s
			ns.mem, opres = op.Exec(ns.mem)
			if op.Type == OpLoad {
				ns.outcome |= Outcome(opres) << op.ID
			}
			ns.pcs[tid]++
			scRec(p, outcomes, ns)
		}
	}
	if !any {
		// This execution is done.
		outcomes.Add(s.outcome)
	}
}
