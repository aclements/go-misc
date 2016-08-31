// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// yuasa is a model of several variants of Yuasa-style deletion
// barriers intended to eliminate stack re-scanning.
package main

import (
	"bytes"
	"fmt"

	"github.com/aclements/go-misc/go-weave/amb"
	"github.com/aclements/go-misc/go-weave/weave"
)

type barrierType int

const (
	// yuasaBarrier is a Yuasa-style deletion barrier. It requires
	// stackBeforeHeap, but does not require rescanStacks.
	yuasaBarrier barrierType = iota

	// dijkstraYuasaBarrier is a combined Dijkstra-style insertion
	// barrier and Yuasa-style deletion barrier. It does not
	// require stackBeforeHeap or rescanStacks.
	dijkstraYuasaBarrier

	// conditionalDijkstraYuasaBarrier is like
	// dijkstraYuasaBarrier before all stacks are blacked, and
	// like yuasaBarrier after stacks have been blacked. It does
	// not require stackBeforeHeap or rescanStacks.
	conditionalDijkstraYuasaBarrier

	// dijkstraBarrier is a Dijkstra-style insertion barrier. It
	// does not require stackBeforeHeap, but it does require
	// rescanStacks.
	dijkstraBarrier
)

// barrier indicates the type of write barrier to use.
const barrier = conditionalDijkstraYuasaBarrier

// stackBeforeHeap indicates that all stacks must be blackened before
// any heap objects are blackened.
const stackBeforeHeap = false

// rescanStacks indicates that stacks must be re-scanned during STW
// mark termination.
const rescanStacks = false

// ptr is a memory pointer, as an index into mem. 0 is the nil
// pointer.
type ptr int

// obj is an object in memory. An object in the "global" or "heap"
// region of memory must not point to an object in the "stack" region
// of memory.
type obj [2]ptr

// mem is the memory, including both the heap and stacks. mem[0] is
// unused (it's the nil slot)
//
// mem[stackBase+i] for i < numThreads is the stack for thread i.
//
// mem[globalRoot] is the global root.
//
// mem[heapBase:] is the heap.
var mem []obj

// marked is the set of mark bits. marked[i] corresponds to mem[i].
var marked []bool

// work is the work list. This is the set of grey objects.
var work []ptr

const numThreads = 2

const stackBase ptr = 1
const globalRoot ptr = stackBase + numThreads
const heapBase ptr = globalRoot + 1
const heapCount = 3

var world weave.RWMutex
var stackLocks [numThreads]weave.Mutex

// rootCount is the number of unscanned roots.
var rootCount int

const verbose = false

var sched = weave.Scheduler{Strategy: &amb.StrategyRandom{}}

func main() {
	sched.Run(func() {
		if verbose {
			print("start:")
		}
		// Create an ambiguous memory.
		//
		// TODO: Tons of these are isomorphic.
		mem = make([]obj, heapBase+heapCount)
		for i := 1; i < len(mem); i++ {
			mem[i] = obj{ambHeapPointer(), ambHeapPointer()}
		}
		marked = make([]bool, len(mem))
		if verbose {
			println(stringMem(mem, marked))
		}
		sched.Tracef("memory: %s", stringMem(mem, marked))
		world = weave.RWMutex{} // Belt and suspenders.
		for i := range stackLocks {
			stackLocks[i] = weave.Mutex{}
		}
		rootCount = numThreads + 1

		// Start mutators.
		for i := 0; i < numThreads; i++ {
			i := i
			sched.Go(func() { mutator(i) })
		}

		if stackBeforeHeap {
			sched.Trace("scanning stacks")
			// Scan stacks and global roots. Complete this
			// before allowing any blackening of the heap.
			for i := stackBase; i < stackBase+numThreads; i++ {
				scan(i)
				marked[i] = true
			}
			scan(globalRoot)
			marked[globalRoot] = true
			sched.Trace("done scanning stacks")
		} else {
			// Grey stacks and global roots. Drain will
			// scan them.
			for i := stackBase; i < stackBase+numThreads; i++ {
				shade(i)
			}
			shade(globalRoot)
		}

		// Blacken heap.
		drain()

		// Wait for write barriers to complete.
		world.Lock()
		defer world.Unlock()

		if rescanStacks {
			sched.Trace("rescanning stacks")
			// Rescan stacks. (The write barrier applies
			// to globals, so we don't need to rescan
			// globalRoot.)
			for i := stackBase; i < stackBase+numThreads; i++ {
				marked[i] = false
				shade(i)
			}
			drain()
			sched.Trace("done rescanning stacks")
		}

		// Check that everything is marked.
		if verbose {
			println(stringMem(mem, marked))
		}
		sched.Tracef("memory: %s", stringMem(mem, marked))
		checkmark()
	})
}

type pointerSet int

const (
	// pointerNil indicates that ambPointer can return a nil
	// pointer.
	pointerNil pointerSet = 1 << iota

	// pointerStack indicates that ambPointer can return a pointer
	// to the stack.
	pointerStack

	// pointerReachable indicates that ambPointer can return a
	// pointer to a reachable heap or global object.
	pointerReachable

	// pointerHeap indicates that ambPointer can return a pointer
	// to any global or heap object.
	pointerHeap
)

// ambPointer returns an ambiguous pointer from the union of the
// specified sets. If ps&(pointerStack|pointerReachable) != 0, tid
// must specify the thread ID of the stack.
func ambPointer(ps pointerSet, tid int) ptr {
	if ps&pointerReachable == 0 {
		// Easy/fast case.
		count := 0
		if ps&pointerNil != 0 {
			count++
		}
		if ps&pointerStack != 0 {
			count++
		}
		if ps&pointerHeap != 0 {
			count += 1 + heapCount
		}
		x := sched.Amb(count)
		if ps&pointerNil != 0 {
			if x == 0 {
				return 0
			}
			x--
		}
		if ps&pointerStack != 0 {
			if x == 0 {
				return stackBase + ptr(tid)
			}
			x--
		}
		if x == 0 {
			return globalRoot
		}
		return heapBase + ptr(x-1)
	}

	// Tricky case. Create a mask of the pointers we're interested in.
	marked := make([]bool, len(mem))
	mark(globalRoot, marked)
	mark(stackBase+ptr(tid), marked)
	if ps&pointerNil != 0 {
		marked[0] = true
	}
	if ps&pointerStack == 0 {
		marked[stackBase+ptr(tid)] = false
	}

	// Select a marked pointer.
	nmarked := 0
	for _, m := range marked {
		if m {
			nmarked++
		}
	}
	x := sched.Amb(nmarked)
	for i, m := range marked {
		if m {
			if x == 0 {
				return ptr(i)
			}
			x--
		}
	}
	panic("not reachable")
}

// ambHeapPointer returns nil or an ambiguous heap or global pointer.
func ambHeapPointer() ptr {
	return ambPointer(pointerNil|pointerHeap, -1)
}

// scan scans obj, shading objects that obj re
func scan(obj ptr) {
	sched.Tracef("scan(%v)", obj)
	if stackBase <= obj && obj < stackBase+numThreads {
		stackLocks[obj-stackBase].Lock()
		defer stackLocks[obj-stackBase].Unlock()
	}
	for i := range mem[obj] {
		p := mem[obj][i]
		sched.Sched()
		shade(p)
	}
	if stackBase <= obj && obj < stackBase+numThreads || obj == globalRoot {
		rootCount--
		sched.Tracef("roots remaining = %d", rootCount)
	}
}

// shade makes obj grey if it is white.
func shade(obj ptr) {
	if obj != 0 && !marked[obj] {
		sched.Tracef("shade(%v)", obj)
		marked[obj] = true
		work = append(work, obj)
	}
}

// drain scans objects in the work queue until the queue is empty.
func drain() {
	for len(work) > 0 {
		// Pick an arbitrary object to scan.
		which := sched.Amb(len(work))
		p := work[which]
		copy(work[which:], work[which+1:])
		work = work[:len(work)-1]

		scan(p)
	}
}

// writePointer implements obj[slot] = val.
func writePointer(obj ptr, slot int, val ptr) {
	// TODO: Check that GC is still running?

	// Synchronize with STW. This blocks STW from happening while
	// we're in the barrier and blocks this goroutine if we're
	// already in STW.
	world.RLock()
	defer world.RUnlock()

	if obj == 0 {
		panic("nil pointer write")
	}

	if stackBase <= obj && obj < stackBase+numThreads {
		mem[obj][slot] = val
		sched.Tracef("stack write %v[%d] = %v", obj, slot, val)
		sched.Sched()
		return
	}

	sched.Tracef("start %v[%d] = %v", obj, slot, val)

	switch barrier {
	case yuasaBarrier:
		old := mem[obj][slot]
		sched.Sched()
		shade(old)

	case dijkstraYuasaBarrier:
		old := mem[obj][slot]
		sched.Sched()
		shade(old)
		shade(val)

	case conditionalDijkstraYuasaBarrier:
		old := mem[obj][slot]
		sched.Sched()
		shade(old)
		if rootCount > 0 {
			shade(val)
		}

	case dijkstraBarrier:
		shade(val)
	}

	mem[obj][slot] = val
	sched.Tracef("done %v[%d] = %v", obj, slot, val)
	sched.Sched()
}

// mutator is a single mutator goroutine running on stack stackBase+tid.
// It shuffles pointers between the heap and stack.
func mutator(tid int) {
	stackptr := stackBase + ptr(tid)

	for i := 0; i < 2; i++ {
		// Take the stack lock to indicate that we're not at a
		// safe point. There's no safe point between reading
		// src and writing pointer since in the model we can't
		// communicate the pointer we're looking at to the GC.
		//
		// Somewhat surprisingly, it's actually necessary to
		// model this. Otherwise stack writes that race with
		// the stack scan can hide pointers.
		stackLocks[tid].Lock()

		// Write a nil, global, or heap pointer to the stack, global,
		// or heap, or a stack pointer to the stack.
		src := ambPointer(pointerNil|pointerStack|pointerReachable, tid)
		sched.Sched()
		var dst ptr
		if src == stackptr {
			// Stack pointers can only be written to the stack.
			dst = stackptr
		} else {
			// Non-stack pointers can be written to stack, global,
			// or heap.
			dst = ambPointer(pointerStack|pointerReachable, tid)
		}
		writePointer(dst, sched.Amb(2), src)

		// We're at a safe point again.
		stackLocks[tid].Unlock()
	}
}

// mark sets marked[i] for every object i reachable from p (including
// p itself). This is NOT preemptible.
func mark(p ptr, marked []bool) {
	if p == 0 || marked[p] {
		return
	}
	marked[p] = true
	for i := range mem[p] {
		mark(mem[p][i], marked)
	}
}

// checkmark checks that all objects reachable from the roots are
// marked.
func checkmark() {
	checkmarked := make([]bool, len(mem))
	for i := stackBase; i < stackBase+numThreads; i++ {
		mark(i, checkmarked)
	}
	mark(globalRoot, checkmarked)

	for i := range marked {
		if checkmarked[i] && !marked[i] {
			panic(fmt.Sprintf("object not marked: %v", i))
		}
	}
}

// stringMem stringifies a memory with marks.
func stringMem(mem []obj, marked []bool) string {
	var buf bytes.Buffer
	for i := 1; i < len(mem); i++ {
		if marked[i] {
			buf.WriteString("*")
		} else {
			buf.WriteString(" ")
		}
		fmt.Fprint(&buf, i, "->", mem[i][0], ",", mem[i][1], " ")
	}
	return buf.String()
}
