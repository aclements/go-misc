// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"
)

const MaxThreads = 3
const MaxOps = 3 // Max ops per thread
const MaxTotalOps = MaxThreads * MaxOps
const MaxVar = MaxTotalOps / 2

type Prog struct {
	Threads  [MaxThreads]Thread
	NumLoads int
}

type Thread struct {
	Ops [MaxOps + 1]Op // Last Op is always OpExit.
}

type Op struct {
	Type OpType
	Var  byte
	ID   byte // For OpLoad, the unique ID of the load.
}

type OpType byte

const (
	OpExit OpType = iota
	OpStore
	OpLoad
)

type PC struct {
	// TID is the thread ID. I is the instruction index.
	TID, I int
}

func (p *Prog) OpAt(pc PC) Op {
	return p.Threads[pc.TID].Ops[pc.I]
}

func (p *Prog) String() string {
	out := []string{}

	line := []string{}
	nthr := len(p.Threads)
	for tid := range p.Threads {
		if p.Threads[tid].Ops[0].Type == OpExit {
			nthr = tid
			break
		}
		line = append(line, fmt.Sprintf("T%d", tid))
	}
	out = append(out, strings.Join(line, "\t"))

	for inst := 0; inst < MaxOps; inst++ {
		line = nil
		any := false
		for tid := range p.Threads[:nthr] {
			if p.Threads[tid].Ops[inst].Type == OpExit {
				line = append(line, "")
				continue
			}
			line = append(line, p.Threads[tid].Ops[inst].String())
			any = true
		}
		if !any {
			break
		}
		out = append(out, strings.Join(line, "\t"))
	}

	return strings.Join(out, "\n")
}

func (o Op) String() string {
	switch o.Type {
	case OpExit:
		return "noop"

	case OpStore:
		return fmt.Sprintf("st %d", o.Var)

	case OpLoad:
		return fmt.Sprintf("%c=ld %d", o.ID+'a', o.Var)
	}
	return "???"
}

// MemState stores the memory state of an executing program. It is a
// bit vector indexed by variable number.
type MemState uint64

func init() {
	if MemState(1<<MaxVar) == 0 {
		panic("MaxVar is too large to fit in MemState")
	}
}

// Exec executes o in state s and returns the new memory state and, if
// the operation is a load, the result of the operation (either 0 or
// 1).
func (o Op) Exec(s MemState) (MemState, int) {
	switch o.Type {
	case OpExit:
		return s, 0
	case OpStore:
		return s | (1 << o.Var), 0
	case OpLoad:
		return s, int((s >> o.Var) & 1)
	}
	panic("bad op")
}

func GenerateProgs() <-chan Prog {
	// TODO: Making this perform well doesn't matter all that much
	// since Prog execution is much more costly, but it would be
	// nice if this generated programs in an order that preferred
	// smaller programs with fewer variables.
	ch := make(chan Prog)
	go func() {
		// TODO: We could take much more advantage of the
		// symmetry between threads and between variables and
		// the fact that each variable only needs to be stored
		// once and that each variable needs at least one
		// store and at least one load.
		//
		// 1. Choose the number of threads >= 2.
		// 2. Choose the number of variables >= 1.
		// 3. Choose the length of the first thread such that
		// threadlen * nthreads >= nvars*2.
		// 4. Generate thread programs as tuples in weakly
		// decreasing order, which each tuple entry is a store
		// or a load of a particular variable. A store always
		// stores the next available variable. This is
		// constrained by step 2 so that if there are no more
		// variables all remaining operations are loads and if
		// the number of remaining variables equals the number
		// of remaining operations, all operations are stores.
		//
		// Other possible constraints: a thread that does just
		// a load is not interesting; each variable needs at
		// least one load on a different thread from the
		// store.
		//
		// Or:
		//
		// 1. Choose the total number of operations >= 2.
		//
		// 2. Choose a "shape" of the program with weakly
		// decreasing number of operations and total area
		// #ops.
		//
		// 3. Choose the number of variables <= floor(#ops/2).
		//
		// 4. Place #variables store operations with
		// increasing positions.
		//
		// 5. For each variable, place a load in a thread that
		// does not store to that variable. (For later
		// variables this may run out of possibilities.)
		//
		// 6. For each remaining instruction slots, place a
		// load of a variable stored on a different thread.

		var p Prog
		genrec(&p, 0, 0, MaxOps, 0, 0, ch)
		close(ch)
	}()
	return ch
}

func genrec(p *Prog, thr, inst, maxops int, nextstore, nextload byte, out chan<- Prog) {
	if thr == MaxThreads {
		// TODO: This check cuts 3x3 down from 2.5M to 15K and
		// 4x3 down from 16.5B to 7.8M, so it really would be
		// worth generating these so as to avoid the silly
		// ones. OTOH, 4x3 only takes 9 minutes to generate,
		// so this might not be the slow part in the end!
		if !silly(p, int(nextstore)) {
			p.NumLoads = int(nextload)
			out <- *p
		}
		return
	}

	nthr, ninst := thr, inst+1
	if ninst == maxops {
		nthr, ninst = nthr+1, 0
	}

	op := &p.Threads[thr].Ops[inst]
	for opt := OpExit; opt <= OpLoad; opt++ {
		op.Type = opt

		switch opt {
		case OpExit:
			op.Var = 0
			if inst == 0 {
				// No more threads.
				if thr < 2 {
					// There need to be at least
					// two threads for this to be
					// interesting.
					continue
				}
				genrec(p, MaxThreads, 0, 0, nextstore, nextload, out)
			} else {
				// Move on to the next thread. Limit
				// it to at most the number of
				// operations in this thread so thread
				// lengths are weakly decreasing.
				genrec(p, thr+1, 0, inst, nextstore, nextload, out)
			}

		case OpLoad:
			op.ID = nextload
			for op.Var = 0; op.Var < MaxVar; op.Var++ {
				// Move on to the next instruction.
				genrec(p, nthr, ninst, maxops, nextstore, nextload+1, out)
			}
			op.ID = 0

		case OpStore:
			// There's one store per variable and since
			// the variable naming doesn't matter, we
			// assign store variables in increasing order.
			if nextstore == MaxVar {
				// Out of variables to store. If we
				// keep going there won't be room for
				// loads of all of the variables.
				continue
			}
			op.Var = nextstore
			// Move on to the next instructions.
			genrec(p, nthr, ninst, maxops, nextstore+1, nextload, out)
		}
	}
	op.Type = OpExit
	op.Var = 0
}

func silly(p *Prog, nvars int) bool {
	var storeThread [MaxVar]int
	for tid := range p.Threads {
		for _, op := range p.Threads[tid].Ops {
			if op.Type == OpStore {
				storeThread[op.Var] = tid
			}
		}
	}
	for tid := range p.Threads {
		for _, op := range p.Threads[tid].Ops {
			if op.Type == OpLoad {
				if storeThread[op.Var] == 0 {
					// Load of a variable never
					// stored. Silly.
					return true
				} else if storeThread[op.Var] != tid {
					storeThread[op.Var] = -1
				}
			}
		}
	}
	for v := 0; v < nvars; v++ {
		if storeThread[v] != -1 {
			// Store without load on another thread. Silly.
			return true
		}
	}
	return false
}
