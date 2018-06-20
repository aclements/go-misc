// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"fmt"
	"io"

	"github.com/aclements/go-misc/obj/internal/asm"
	"github.com/aclements/go-misc/obj/internal/graph"
)

type Func struct {
	// Seq is the sequence of instructions that constitute this
	// function.
	Seq asm.Seq

	// Blocks is a list of SSA basic blocks in this function.
	//
	// The IDs of the SSA basic blocks are the same as the
	// assembly basic blocks.
	//
	// Blocks[0] is the entry block. It always has no
	// predecessors, and will begin with OpEntry Values for
	// assembly variables that are used on entry to the function.
	Blocks []*BasicBlock
}

type BasicBlock struct {
	// Src is the assembly basic block this basic block
	// corresponds to.
	Src *asm.BasicBlock

	// Values is the sequence of SSA value computations in this
	// block. These are ordered the same as the assembly
	// operations they reflect (and hence appear in dependency
	// order).
	//
	// Blocks always begin with either OpPhi or OpEntry values,
	// followed by OpInst values.
	Values []*Value
}

type Value struct {
	Op Op

	// ID is a small, dense numbering of Values unique within a
	// Func.
	ID int

	// Entry is a value from outside this function. Valid if Op ==
	// OpEntry.
	Entry asm.Loc

	// Inst is the assembly instruction index that computes this
	// value. Valid if Op == OpInst.
	Inst int

	// Args are the Values read by this operation.
	//
	// TODO: What if it writes multiple values? Maybe all values
	// are implicitly tuple-like and arguments are a Value/index
	// pair. Or maybe this includes both the Value and the
	// underlying assembly variable. Or maybe I dump the index and
	// don't care.
	Args []*Value
}

type Op uint8

const (
	OpPhi Op = 1 + iota
	OpEntry
	OpInst
)

// SSA computes an single static assignment (SSA) form of seq. This
// augments seq with single-assignment values, and the necessary phi
// operations. Each instruction in seq becomes an SSA "value"
// representing the result of that instruction. The SSA representation
// records which values become arguments to each instruction.
//
// The asmBlocks argument must be the result of asm.BasicBlocks on seq.
func SSA(seq asm.Seq, asmBlocks []*asm.BasicBlock) *Func {
	// See https://www.seas.harvard.edu/courses/cs252/2011sp/slides/Lec04-SSA.pdf
	// for a good overview of the SSA construction algorithm.

	// TODO: Model stack slots. Model calls.

	// Get the assembly blocks that will be used in the SSA and
	// create SSA basic blocks. asm.BasicBlocks guarantees the
	// entry node will have no successors.
	blocks := make([]*BasicBlock, len(asmBlocks))
	for i := range blocks {
		blocks[i] = &BasicBlock{Src: asmBlocks[i]}
	}
	fn := &Func{seq, blocks}

	// Compute the dominator tree.
	idom := graph.IDom(asm.BasicBlockGraph(asmBlocks), 0)
	dom := graph.Dom(idom)

	// Compute the dominance frontier for phi placement.
	df := graph.DomFrontier(asm.BasicBlockGraph(asmBlocks), 0, idom)

	// Collect the read/write sets of all instructions.
	type rwSet struct{ r, w []asm.Loc }
	effects := make([]rwSet, seq.Len())
	for i := range effects {
		r, w := seq.Get(i).Effects()
		if w.Has(asm.LocMem) {
			// All mem writes are, in effect, partial, so
			// if an operation writes part of mem, it also
			// has to pass through the rest of mem.
			r.Add(asm.LocMem)
		}
		effects[i].r, effects[i].w = r.Ordered(), w.Ordered()
	}

	// Place phis. We compute the iterated dominance frontier
	// on-the-fly as we're doing this.
	phiLocs := map[*Value]asm.Loc{}
	var addPhis func(bdf []int, loc asm.Loc)
	addPhis = func(bdf []int, loc asm.Loc) {
	nextBlock:
		for _, bi := range bdf {
			b := blocks[bi]

			// Add the phi to b if it doesn't already have
			// one for this variable.
			for _, v := range b.Values {
				if v.Op == OpPhi && phiLocs[v] == loc {
					continue nextBlock
				}
			}
			phiArgs := make([]*Value, len(b.Src.Preds))
			phi := &Value{Op: OpPhi, Args: phiArgs}
			phiLocs[phi] = loc
			b.Values = append(b.Values, phi)

			// Iterate on the dominance frontier since we
			// just added a definition.
			addPhis(df[bi], loc)
		}
	}
	for bi, b := range blocks {
		for i := b.Src.Start; i < b.Src.End; i++ {
			for _, w := range effects[i].w {
				// Add phi for variable w to b's DF.
				addPhis(df[bi], w)
			}
		}
	}

	// Transform assembly variables into SSA values by walking the
	// dominator tree.
	type undo struct {
		k asm.Loc
		v *Value
	}
	vals := make(map[asm.Loc]*Value)
	undoStack := []undo{}
	entryValues := []*Value{}
	addEntry := func(e asm.Loc) {
		// Synthesize an entry value. Don't put this in the
		// undo stack since it's actually introduced by the
		// entry block.
		val := &Value{Op: OpEntry, Entry: e}
		vals[e] = val
		entryValues = append(entryValues, val)
	}
	var walk func(node int)
	walk = func(node int) {
		b := blocks[node]
		undoPos := len(undoStack)

		// Transform phis.
		for _, val := range b.Values {
			w := phiLocs[val]
			undoStack = append(undoStack, undo{w, vals[w]})
			vals[w] = val
		}

		// Transform instructions/variables.
		for i := b.Src.Start; i < b.Src.End; i++ {
			val := &Value{Op: OpInst, Inst: i}
			b.Values = append(b.Values, val)

			// Map read set into argument values.
			for _, r := range effects[i].r {
				if vals[r] == nil {
					addEntry(r)
				}
				val.Args = append(val.Args, vals[r])
			}

			// Map write set into new values.
			for _, w := range effects[i].w {
				undoStack = append(undoStack, undo{w, vals[w]})
				vals[w] = val
			}
		}

		// Fill in arguments to phis in successor nodes, now
		// that we know the outgoing values for each variable.
		for _, edge := range b.Src.Succs {
			succ := blocks[edge.Block.ID]
			for _, phi := range succ.Values {
				if phi.Op != OpPhi {
					break
				}

				phiLoc := phiLocs[phi]
				if vals[phiLoc] == nil {
					addEntry(phiLoc)
				}
				phi.Args[edge.RIndex] = vals[phiLoc]
			}
		}

		// Continue the depth-first walk of the dominator tree.
		for _, child := range dom.Out(node) {
			walk(child)
		}

		// Pop mappings.
		for len(undoStack) > undoPos {
			undo := undoStack[len(undoStack)-1]
			undoStack = undoStack[:len(undoStack)-1]
			vals[undo.k] = undo.v
		}
	}
	walk(0)

	// Combine the entry values into the entry block.
	blocks[0].Values = append(entryValues, blocks[0].Values...)

	// Prune unused synthetic values (OpPhi and OpEntry) by
	// flooding uses from actual values (OpInst). We use the Inst
	// field for this mark.
	var floodUse func(val *Value)
	floodUse = func(val *Value) {
		if !(val.Op == OpPhi || val.Op == OpEntry) {
			return
		}
		if val.Inst != 0 {
			return
		}
		val.Inst = 1
		for _, arg := range val.Args {
			floodUse(arg)
		}
	}
	for _, b := range blocks {
		for _, val := range b.Values {
			if val.Op == OpPhi || val.Op == OpEntry {
				continue
			}
			for _, arg := range val.Args {
				floodUse(arg)
			}
		}
	}
	// Delete unmarked values and clear Inst.
	for _, b := range blocks {
		j := 0
		for i, val := range b.Values {
			if val.Op == OpPhi || val.Op == OpEntry {
				if val.Inst != 0 {
					val.Inst = 0
					b.Values[j] = val
					j++
				}
			} else {
				if j != i {
					j += copy(b.Values[j:], b.Values[i:])
				} else {
					j = len(b.Values)
				}
				break
			}
		}
		b.Values = b.Values[:j]
	}

	// Number values.
	n := 0
	for _, b := range blocks {
		for _, val := range b.Values {
			val.ID = n
			n++
		}
	}

	return fn
}

// Fprint writes a pretty representation of the basic blocks and
// values of f to w.
func (f *Func) Fprint(w io.Writer) {
	for _, bb := range f.Blocks {
		fmt.Fprintf(w, "b%d <-", bb.Src.ID)
		for _, edge := range bb.Src.Preds {
			fmt.Fprintf(w, " b%d", edge.Block.ID)
		}
		fmt.Fprintf(w, "\n")

		for _, val := range bb.Values {
			fmt.Fprintf(w, "v%d = ", val.ID)
			switch val.Op {
			case OpPhi:
				fmt.Fprintf(w, "phi")
			case OpEntry:
				fmt.Fprintf(w, "entry %v", val.Entry)
			case OpInst:
				fmt.Fprintf(w, "inst [%s]", f.Seq.Get(val.Inst).GoSyntax(nil))
			}
			for _, arg := range val.Args {
				fmt.Fprintf(w, " v%d", arg.ID)
			}
			fmt.Fprintf(w, "\n")
		}
		fmt.Fprintf(w, "->")
		for _, edge := range bb.Src.Succs {
			fmt.Fprintf(w, " b%d", edge.Block.ID)
		}
		fmt.Fprintf(w, "\n\n")
	}
}
