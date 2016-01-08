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
}

func (m TSOModel) String() string {
	s := "TSO"
	if m.StoreMFence {
		s += "+store MFENCE"
	}
	return s
}

type TSOState struct {
	// Program state.
	mem MemState
	sb  [MaxThreads]struct {
		// Per-CPU store buffer
		overlay MemState
		buf     [MaxOps]byte
		h, t    int
	}
	pcs     [MaxThreads]int
	outcome Outcome
}

func (m TSOModel) Eval(p *Prog, outcomes *OutcomeSet) {
	outcomes.Reset(p)
	m.tsoRec(p, outcomes, TSOState{})
}

func (m TSOModel) tsoRec(p *Prog, outcomes *OutcomeSet, s TSOState) {
	// Pick an op to execute next.
	var opres int
	any := false
	for tid := range p.Threads {
		op := p.Threads[tid].Ops[s.pcs[tid]]
		if op.Type != OpExit {
			any = true
			ns := s
			sb := &ns.sb[tid]
			switch op.Type {
			case OpLoad:
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

				if m.StoreMFence {
					// Flush the store buffer.
					ns.mem |= sb.overlay
					sb.h, sb.t = 0, 0
				}
			}
			ns.pcs[tid]++
			m.tsoRec(p, outcomes, ns)
		}
	}
	if !any {
		// This execution is done. We don't care if there's
		// stuff in the store buffers.
		outcomes.Add(s.outcome)
		return
	}

	// Pick a store buffer to pop.
	for tid := range p.Threads {
		if s.sb[tid].h < s.sb[tid].t {
			ns := s
			sb := &ns.sb[tid]
			ns.mem |= MemState(1 << sb.buf[sb.h])
			sb.h++
			m.tsoRec(p, outcomes, ns)
		}
	}
}
