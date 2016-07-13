// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

package main

import (
	"fmt"

	"github.com/aclements/go-misc/go-weave/amb"
	"github.com/aclements/go-misc/go-weave/weave"
)

// writeMarks indicates that the write barrier should transitively
// mark objects before publishing them.
const writeMarks = true

// writeRestarts indicates that the write barrier should reset the
// stack scan.
const writeRestarts = false

// ptr is a memory pointer, as an index into mem. 0 is the nil
// pointer.
type ptr int

// obj is an object in memory. An object in the "heap" region of
// memory must not point to an object in the "stack" region of memory.
type obj struct {
	l, r ptr
}

// mem is the memory, including both the heap and stacks. mem[0] is
// unused (it's the nil slot). mem[stackBase:stackBase+numThreads] are
// the stacks. mem[globalRoot:] is the heap. mme[globalRoot] is the
// global root.
var mem []obj

var marked []bool

const numThreads = 2

const stackBase ptr = 1
const globalRoot ptr = stackBase + ptr(numThreads)

var scanClock int
var busy int

const verbose = false

func main() {
	weave.Run(func() {
		if verbose {
			print("start:")
		}
		// Create an ambiguous memory.
		//
		// TODO: Tons of these are isomorphic.
		mem = make([]obj, 6)
		for i := 1; i < len(mem); i++ {
			mem[i].l = ambHeapPointer()
			if ptr(i) >= globalRoot {
				// For stacks we only use l.
				mem[i].r = ambHeapPointer()
			}
		}
		marked = make([]bool, len(mem))
		if verbose {
			printMem(mem, marked)
		}
		scanClock = 0

		// Mark the global root.
		mark(globalRoot, marked, "globalRoot")

		// Start mutators.
		for i := 0; i < numThreads; i++ {
			i := i
			weave.Go(func() { mutator(i) })
		}

		// Re-scan stacks.
		for scanClock < numThreads {
			if verbose {
				println("scan", scanClock)
			}
			scanClock++
			mark(mem[stackBase+ptr(scanClock-1)].l, marked, "scan")
		}
		for busy > 0 {
			// XXX This has liveness problems.
			if verbose {
				println("waiting on barrier", busy)
			}
			weave.Sched()
		}

		// Check that everything is marked.
		if verbose {
			printMem(mem, marked)
		}
		checkmark(globalRoot)
		for i := 0; i < numThreads; i++ {
			checkmark(mem[stackBase+ptr(i)].l)
		}
	})
}

// ambHeapPointer returns nil or an ambiguous heap pointer.
func ambHeapPointer() ptr {
	x := amb.Amb(len(mem) - int(globalRoot) + 1)
	if x == 0 {
		return 0
	}
	return ptr(x-1) + globalRoot
}

// ambReachableHeapPointer returns an ambiguous reachable heap
// pointer. Note that the object may not be marked.
func ambReachableHeapPointer() ptr {
	reachable := make([]bool, len(mem))
	mark(globalRoot, reachable, "")

	nreachable := 0
	for _, m := range reachable[globalRoot:] {
		if m {
			nreachable++
		}
	}
	x := amb.Amb(nreachable)
	for i, m := range reachable[globalRoot:] {
		if m {
			if x == 0 {
				return globalRoot + ptr(i)
			}
			x--
		}
	}
	panic("not reached")
}

func wbarrier(slot, val ptr) {
	// TODO: Check that GC is still running?

	// TODO: Need to mark val regardless (but doesn't have to be
	// transitive).

	if val != 0 {
		if writeMarks {
			busy++
			mark(mem[val].l, marked, "barrier")
			busy--
		}
		if writeRestarts {
			if !marked[val] {
				scanClock = 0
			}
		}
	}
	mem[slot].l = mem[val].l
	weave.Sched()
}

func mutator(id int) {
	sptr := stackBase + ptr(id)

	// TODO: nil pointer writes?

	// Publish our stack pointer to some live heap object.
	obj := ambReachableHeapPointer()
	//mem[obj].l = mem[sptr].l
	if verbose {
		print(obj, ".l = ", mem[sptr].l, "\n")
	}
	wbarrier(obj, sptr)
	if verbose {
		print(obj, ".l = ", mem[sptr].l, " done\n")
	}

	// Read a pointer from the heap. No write barrier since this
	// is a stack write.
	obj = ambReachableHeapPointer()
	mem[sptr].l = mem[obj].l
	weave.Sched()
}

func mark(p ptr, marked []bool, name string) {
	if p == 0 || marked[p] {
		return
	}
	marked[p] = true
	if name != "" {
		if verbose {
			println(name, "marked", p)
		}
	}
	mark(mem[p].l, marked, name)
	if name != "" {
		weave.Sched()
	}
	mark(mem[p].r, marked, name)
	if name != "" {
		weave.Sched()
	}
}

func checkmark(p ptr) {
	checkmarked := make([]bool, len(mem))
	var mark1 func(p ptr)
	mark1 = func(p ptr) {
		if p == 0 {
			return
		}
		if !marked[p] {
			panic(fmt.Sprintf("object not marked: %d", p))
		}
		if checkmarked[p] {
			return
		}
		checkmarked[p] = true
		mark1(mem[p].l)
		mark1(mem[p].r)
	}
	mark1(p)
}

func printMem(mem []obj, marked []bool) {
	for i := 1; i < len(mem); i++ {
		if marked[i] {
			print("*")
		} else {
			print(" ")
		}
		print(i, "->", mem[i].l, ",", mem[i].r, " ")
	}
	println()
}
