// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

type TSOVariant int

// TSOModel models all loads and stores as TSO operations, possibly
// with additional barriers. This implements TSO using the abstract
// machine model of Sewell, et al., "x86-TSO: A Rigorous and Usable
// Programmer’s Model for x86 Multiprocessors", CACM Research
// Highlights, 2010.
type TSOModel struct {
	// StoreMFence, if true, adds an MFENCE after store
	// operations.
	StoreMFence bool

	// MFenceLoad, if true, add an MFENCE before load operations.
	MFenceLoad bool
}

func (m TSOModel) String() string {
	s := "TSO"
	if m.StoreMFence {
		s += "+store MFENCE"
	}
	if m.MFenceLoad {
		s += "+MFENCE load"
	}
	return s
}

func (m TSOModel) Eval(p *Prog, outcomes *OutcomeSet) {
	outcomes.Reset(p)
	(&tsoGlobal{p, outcomes, &m}).rec(tsoState{})
}

// tsoGlobal stores state that is global to a TSO evaluation.
type tsoGlobal struct {
	p        *Prog
	outcomes *OutcomeSet
	model    *TSOModel
}

// tsoState stores the state of a program at a single point during
// execution.
type tsoState struct {
	mem MemState             // Global memory state.
	sb  [MaxThreads]struct { // Per-CPU store buffer.
		// overlay records all stores performed by this CPU.
		overlay MemState
		// buf, h, and t are the store buffer FIFO.
		buf  [MaxOps]byte
		h, t int
	}
	pcs     [MaxThreads]int
	outcome Outcome
}

func (g *tsoGlobal) rec(s tsoState) {
	// Pick an op to execute next.
	var opres int
	any := false
	for tid := range g.p.Threads {
		op := g.p.Threads[tid].Ops[s.pcs[tid]]
		if op.Type != OpExit {
			any = true
			ns := s
			sb := &ns.sb[tid]
			switch op.Type {
			case OpLoad:
				if g.model.MFenceLoad {
					// Flush the store buffer.
					ns.mem |= sb.overlay
					sb.h, sb.t = 0, 0
				}

				// Combining the global memory and the
				// overlay simulates store buffer
				// forwarding.
				_, opres = op.Exec(ns.mem | sb.overlay)
				ns.outcome |= Outcome(opres) << op.ID
			case OpStore:
				// Write to the store buffer.
				sb.overlay, _ = op.Exec(sb.overlay)
				sb.buf[sb.t] = op.Var
				sb.t++

				if g.model.StoreMFence {
					// Flush the store buffer.
					ns.mem |= sb.overlay
					sb.h, sb.t = 0, 0
				}
			}
			ns.pcs[tid]++
			g.rec(ns)
		}
	}
	if !any {
		// This execution is done. We don't care if there's
		// stuff in the store buffers.
		g.outcomes.Add(s.outcome)
		return
	}

	// Pick a store buffer to pop.
	for tid := range g.p.Threads {
		if s.sb[tid].h < s.sb[tid].t {
			ns := s
			sb := &ns.sb[tid]
			ns.mem |= MemState(1 << sb.buf[sb.h])
			sb.h++
			g.rec(ns)
		}
	}
}
