// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asm

import (
	"fmt"
	"math/big"
	"sort"
)

// TODO: Put this in another package?

// A BasicBlock represents a sequence of instructions that have no
// control flow entry points, and only have a control flow exit point
// from the end of the sequence.
type BasicBlock struct {
	// ID is the index of this BasicBlock in the list of basic
	// blocks.
	ID int

	// Start and End give the range of instructions in this basic
	// block. The block is instructions [Start, End).
	Start, End int

	// Control describes the exit from this basic block. For plain
	// basic blocks that have a single exit to another basic
	// block, this will have Type ControlNone.
	Control Control

	// Succs is the successors of this basic block. Different
	// types of blocks have different numbers of successors:
	//
	// Type  len(Succs)
	// None  1
	// Jump  1 or 2 depending on Control.Conditional
	// Ret   0 or 1 depending on Control.Conditional
	// Exit  0 or 1 depending on Control.Conditional
	//
	// TODO: Be consistent about true/false order?
	Succs []Edge

	// Preds is the predecessors of this basic block.
	Preds []Edge

	succStore [2]Edge
	predStore [4]Edge
}

// An Edge is a control-flow edge (either successor or predecessor)
// between two BasicBlocks.
type Edge struct {
	// Block is the target of this edge. If this is a successor
	// edge, Block is a successor BasicBlock. Otherwise, it's a
	// predecessor BasicBlock.
	Block *BasicBlock

	// RIndex is the index of this edge in the reverse direction.
	// E.g., if this is a successor edge, this is the index of the
	// source block in the successor's Preds slides.
	RIndex int
}

// BasicBlocks returns the basic blocks in seq. The entry basic block
// is always block 0. For ease of analysis, the result always has at
// least one block and the entry block always has no predecessors (an
// empty block will be created if necessary). Unreachable blocks do
// not appear in the output.
//
// If the control-flow graph cannot be computed, this returns an
// error. Currently, the only reason for this is computed jumps.
func BasicBlocks(seq Seq) ([]*BasicBlock, error) {
	// Find the start of each basic block.
	var startPCs []uint64
	pcs := make(map[uint64]int, seq.Len())
	newBlock := true
	for i := 0; i < seq.Len(); i++ {
		inst := seq.Get(i)
		pc := inst.PC()
		pcs[pc] = i

		if newBlock {
			startPCs = append(startPCs, pc)
			newBlock = false
		}

		c := inst.Control()
		switch c.Type {
		case ControlJump:
			if c.TargetPC == 0 {
				return nil, fmt.Errorf("jump with unknown target: %s", inst)
			}
			startPCs = append(startPCs, c.TargetPC)
			newBlock = true
		case ControlRet, ControlExit:
			newBlock = true
		}
	}

	// Sort and dedup starts so we can break the sequence into
	// basic blocks.
	sort.Slice(startPCs, func(i, j int) bool { return startPCs[i] < startPCs[j] })
	{
		j, prev := 0, uint64(0)
		for i, pc := range startPCs {
			// Remove starts outside of this sequence
			// (e.g., tail calls to other functions).
			if _, ok := pcs[pc]; !ok {
				continue
			}
			if i == 0 || pc != prev {
				startPCs[j] = pc
				j++
				prev = pc
			}
		}
		startPCs = startPCs[:j]
	}

	// Construct the basic blocks.
	bbs := make([]*BasicBlock, 0, 1+len(startPCs))
	bbPCs := make(map[uint64]*BasicBlock, 1+len(startPCs))
	bbs = append(bbs, &BasicBlock{Control: Control{Type: ControlNone}})
	bbs[0].Succs = bbs[0].succStore[:0]
	bbs[0].Preds = bbs[0].predStore[:0]
	for i, startPC := range startPCs {
		start := pcs[startPC]
		var end int
		if i+1 < len(startPCs) {
			end = pcs[startPCs[i+1]]
		} else {
			end = seq.Len()
		}

		bb := &BasicBlock{
			ID:      len(bbs),
			Start:   start,
			End:     end,
			Control: seq.Get(end - 1).Control(),
		}
		bb.Succs = bb.succStore[:0]
		bb.Preds = bb.predStore[:0]
		bbs = append(bbs, bb)

		bbPCs[seq.Get(start).PC()] = bb
	}

	if len(bbs) == 1 {
		bbs[0].Control = Control{Type: ControlExit}
		return bbs, nil
	}

	// Add control-flow edges.
	addEdge := func(from, to *BasicBlock) {
		from.Succs = append(from.Succs, Edge{to, len(to.Preds)})
		to.Preds = append(to.Preds, Edge{from, len(from.Succs) - 1})
	}
	for i, bb := range bbs {
		next := false
		var alt *BasicBlock

		switch bb.Control.Type {
		case ControlNone, ControlCall:
			next = true

		case ControlJump:
			if bb.Control.Conditional {
				next = true
			}
			tbb, ok := bbPCs[bb.Control.TargetPC]
			if !ok {
				// Jump outside function. Turn this
				// into an exit.
				bb.Control.Type = ControlExit
			} else {
				alt = tbb
			}

		case ControlRet, ControlExit:
			if bb.Control.Conditional {
				next = true
			}

		default:
			panic(fmt.Sprintf("bad control type %s", bb.Control.Type))
		}

		if next {
			if i+1 < len(bbs) {
				addEdge(bb, bbs[i+1])
			} else {
				// Turn this into an exit block. This
				// must mean that the block ends with
				// a no-return call.
				bb.Control.Type = ControlExit
			}
		}
		if alt != nil {
			addEdge(bb, alt)
		}
	}

	// Delete unreachable blocks.
	var reachable big.Int
	nReachable := 0
	var mark func(node int)
	mark = func(node int) {
		if reachable.Bit(node) != 0 {
			return
		}
		reachable.SetBit(&reachable, node, 1)
		nReachable++
		for _, succ := range bbs[node].Succs {
			mark(succ.Block.ID)
		}
	}
	mark(0)
	if nReachable != len(bbs) {
		// Remove unreachable blocks.
		j := 0
		for i, bb := range bbs {
			if reachable.Bit(i) != 0 {
				bbs[j] = bb
				j++
			}
		}
		for i := j; i < len(bbs); i++ {
			bbs[i] = nil
		}
		bbs = bbs[:j]

		// Filter predecessor lists.
		for _, bb := range bbs {
			j := 0
			for _, pred := range bb.Preds {
				if reachable.Bit(pred.Block.ID) != 0 {
					bb.Preds[j] = pred
					pred.Block.Succs[pred.RIndex].RIndex = j
					j++
				}
			}
			for i := j; i < len(bb.Preds); i++ {
				bb.Preds[i] = Edge{}
			}
			bb.Preds = bb.Preds[:j]
		}

		// Re-number blocks now that we're done with the
		// reachable set.
		for i, bb := range bbs {
			bb.ID = i
		}
	}

	return bbs, nil
}

type BasicBlockGraph []*BasicBlock

func (g BasicBlockGraph) NumNodes() int {
	return len(g)
}

func (g BasicBlockGraph) Out(i int) []int {
	out := make([]int, len(g[i].Succs))
	for i, edge := range g[i].Succs {
		out[i] = edge.Block.ID
	}
	return out
}

func (g BasicBlockGraph) In(i int) []int {
	out := make([]int, len(g[i].Preds))
	for i, edge := range g[i].Preds {
		out[i] = edge.Block.ID
	}
	return out
}
