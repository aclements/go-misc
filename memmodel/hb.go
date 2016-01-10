// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "fmt"

// HBModel is a generalized memory model with a plug-in happens-before
// graph generator.
//
// This model works by generating all sequential interleavings of the
// program, constructing the happens-before graph for each
// interleaving, and computing the possible outcomes for each load
// operation based on the happens-before graph (notably, not based on
// the sequential order). If a load happens concurrently with a store
// (that is, it neither happens before nor happens after), it can read
// either 0 or 1, so each sequential interleaving can produce one or
// more outcomes.
type HBModel struct {
	Gen HBGenerator
}

func (m HBModel) String() string {
	return m.Gen.String() + " (HB)"
}

// HBGenerator generates a happens-before graph.
//
// TODO: Given how stateless this is, we could precompute all n^2
// results.
type HBGenerator interface {
	// HappensBefore returns true if instruction i globally
	// happens before instruction j, assuming that i was executed
	// before j.
	//
	// It does not necessarily return all happens-before edges;
	// only enough so that the transitive closure of the returned
	// graph is the full global happens-before graph.
	//
	// It does not return program order edges unless they are part
	// of the global happens before graph. Each thread implicitly
	// has a local happens before graph that combines the global
	// happens before graph with the local program order.
	HappensBefore(p *Prog, i, j PC) bool

	// String returns a string representation of this HBGenerator.
	String() string
}

func (m HBModel) Eval(p *Prog, outcomes *OutcomeSet) {
	var seqBuf [MaxTotalOps]PC
	var nodeBuf [MaxTotalOps]hbGraphNode
	g := hbGlobal{
		p:        p,
		outcomes: outcomes,
		model:    m,
		sequence: seqBuf[:0],
		graph:    hbGraph{nodeBuf[:0]},
	}
	outcomes.Reset(p)
	g.rec(hbState{})
}

// hbGlobal stores state that is global to an evaluation.
type hbGlobal struct {
	p        *Prog
	outcomes *OutcomeSet
	model    HBModel

	// sequence is the current program counter sequence. PCs are
	// pushed and popped as rec explores interleavings.
	sequence []PC

	// graph is the current happens-before graph. This is built up
	// together with sequence.
	graph hbGraph

	// vars records the indexes in sequence of loads and stores
	// that have executed on each variable.
	vars [MaxVar]varInfo
}

// An hbGraphNode represents the set of in-edges for a single node in
// the global happens-before graph. If bit i is set, then node i
// globally happens-before this node.
type hbGraphNode uint16

func init() {
	if hbGraphNode(1<<MaxTotalOps) == 0 {
		panic("MaxTotalOps is too large to fit in hbGraphNode")
	}
}

// An hbGraph is a global happens-before graph represented in matrix
// form. Nodes correspond to instructions and are indexed by their
// position in the execution sequence. Each thread has a local
// happens-before graph that is the union of the global happens-before
// graph and the local program order.
type hbGraph struct {
	nodes []hbGraphNode
}

// happenedBefore returned true if i happened before j.
func (g *hbGraph) happenedBefore(i, j int) bool {
	return g.nodes[j]&(1<<uint(i)) != 0
}

type varInfo struct {
	// stores are the indexes of store operations to this variable
	// in sequence.
	stores []int

	// loads are the indexes of load operations from this variable
	// in sequence.
	loads []int
}

// hbState stores the state of a program at a single point during
// execution.
type hbState struct {
	// Program state.
	pcs [MaxThreads]int

	// allow0 and allow1 indicate whether each load can return 0
	// or 1, respectively. It's possible that a load can return
	// either, in which case both will be set. By the end of an
	// execution, at least of the two possibilities will be set
	// for each load operation.
	allow0, allow1 Outcome
}

func (g *hbGlobal) rec(s hbState) {
	// To reduce repeated work, we construct the happens-before
	// graph and outcome set incrementally and as early in the
	// recursion as possible. The happens-before graph must be
	// consistent with the sequential order, so every time we
	// select the next instruction, we can immediately add it to
	// the happens-before graph and compute its full set of
	// in-edges (because these must all come from already executed
	// instructions).
	//
	// Because we build the graph incrementally, we can also
	// compute possible outcomes incrementally. For each load, we
	// need to answer whether the store to that variable happened
	// before, after, or concurrently with the load. When we
	// execute the load, if the store has already been executed,
	// we can answer whether it happened before or concurrently
	// with the load. If the store hasn't been executed yet, it
	// may happen after or concurrently with the load; once we
	// execute the store, we go back and determine this for all of
	// the previously executed loads of that variable.

	// Pick an op to execute next.
	any := false
	for tid := range g.p.Threads {
		op := g.p.Threads[tid].Ops[s.pcs[tid]]
		if op.Type == OpExit {
			continue
		}

		any = true
		ns := s
		thisPC := PC{tid, s.pcs[tid]}
		g.sequence = append(g.sequence, thisPC)

		// Add a node for this instruction to the global
		// happens-before graph.
		var node hbGraphNode
		this := len(g.sequence) - 1
		// Compute the in-edges and the transitive closure.
		for prev := this - 1; prev >= 0; prev-- {
			if node&(1<<uint(prev)) != 0 {
				// By transitive closure, we already
				// know prev happens-before this.
				continue
			}

			if g.model.Gen.HappensBefore(g.p, g.sequence[prev], thisPC) {
				// Add global "prev -> this" edge and
				// the transitive closure.
				node |= (1 << uint(prev)) | g.graph.nodes[prev]
			}
		}
		g.graph.nodes = append(g.graph.nodes, node)

		var varOpArray *[]int
		switch op.Type {
		case OpStore:
			// Find loads to this variable that have
			// already executed.
			for _, ld := range g.vars[op.Var].loads {
				// If this load locally happened
				// before the store, the load must be
				// 0 (which we've already recorded).
				if g.sequence[ld].TID == tid || g.graph.happenedBefore(ld, this) {
					continue
				}
				// Otherwise, the load happened
				// concurrently with the store, so it
				// can also be 1.
				ldpc := g.sequence[ld]
				ns.allow1.Set(g.p.OpAt(ldpc), 1)
			}

			varOpArray = &g.vars[op.Var].stores

		case OpLoad:
			// Find stores to this variable that have
			// already executed.
			var mustBe1, any bool
			for _, st := range g.vars[op.Var].stores {
				any = true
				// If this store locally happened
				// before the load, the load must be
				// 1.
				if g.sequence[st].TID == tid || g.graph.happenedBefore(st, this) {
					mustBe1 = true
					break
				}
			}

			if mustBe1 {
				ns.allow1.Set(op, 1)
			} else if any {
				// There were stores, but none
				// happened before the load, then the
				// load happened concurrently with the
				// stores, so it may be 0 or 1.
				ns.allow0.Set(op, 1)
				ns.allow1.Set(op, 1)
			} else {
				// Otherwise, it's definitely possible
				// for it to be 0. There may also be a
				// future store that happens
				// concurrently; if we find one, we'll
				// allow 1 at that point.
				ns.allow0.Set(op, 1)
			}

			varOpArray = &g.vars[op.Var].loads

		default:
			panic("unknown op")
		}

		// Associate this operation with the variable.
		*varOpArray = append(*varOpArray, this)

		ns.pcs[tid]++
		g.rec(ns)

		g.sequence = g.sequence[:len(g.sequence)-1]
		g.graph.nodes = g.graph.nodes[:len(g.graph.nodes)-1]
		*varOpArray = (*varOpArray)[:len(*varOpArray)-1]
	}
	if !any {
		// This execution is done. Expand out the full set of
		// possible outcomes.
		if (s.allow0|s.allow1)>>uint(g.p.NumLoads) != 0 {
			panic("more outcome bits than loads")
		}
		if (s.allow0^(1<<uint(g.p.NumLoads)-1))&(s.allow1^(1<<uint(g.p.NumLoads)-1)) != 0 {
			panic(fmt.Sprintf("no outcome for load: allow0=0x%x allow1=0x%x in program:\n%s", s.allow0, s.allow1, g.p))
		}
		g.addOutcomes(0, s.allow0, s.allow1, 0)
	}
}

func (g *hbGlobal) addOutcomes(bit uint, allow0, allow1, out Outcome) {
	if (allow0&allow1)>>bit == 0 {
		// The rest are deterministic (or we're at the end).
		out |= allow1 >> bit << bit
		g.outcomes.Add(out)
		return
	}

	// Copy deterministic bits.
	for (allow0&allow1)&(1<<bit) == 0 {
		if allow1&(1<<bit) != 0 {
			out |= 1 << bit
		}
		bit++
	}

	// Bit is now non-deterministic. Go both ways.
	g.addOutcomes(bit+1, allow0, allow1, out)
	g.addOutcomes(bit+1, allow0, allow1, out|(1<<bit))
}

// SC: i happens before j if and only if i < j.

// TSO: i happens before j if i < j and 1) both i and j are stores, or
// 2) both i and j are loads, or 3) i is a load and j is a store, or
// 4) i stores to X and j loads from X.

// RMO: i happens before j if i < j and i stores to X and j loads from
// X.
