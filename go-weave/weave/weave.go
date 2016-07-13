// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

import (
	"fmt"
	"runtime"
	"sort"

	"github.com/aclements/go-misc/go-weave/amb"
)

type thread struct {
	id      int
	wake    chan bool
	schedPC uintptr
}

func (t *thread) String() string {
	return fmt.Sprintf("t%d", t.id)
}

var nextid int
var threads []*thread
var runThread, curThread *thread
var goErr interface{}
var Monitor func()

type pcmap struct {
	states map[interface{}]struct{}
	next   map[uintptr]*pcmap
}

var rootPCMap pcmap
var allPCs = []int{}
var State func() interface{}

const debug = false

func Run(main func()) {
	amb.Run(func() {
		nextid = 0
		threads = nil
		runThread = &thread{-1, make(chan bool), 0}
		curThread = runThread
		goErr = nil
		Monitor = nil
		Go(main)
		if goErr != nil {
			panic(goErr)
		}
		if debug {
			fmt.Println("run done")
		}
	})
	rootPCMap = pcmap{}
}

func Go(f func()) {
	thr := &thread{nextid, make(chan bool), ^uintptr(nextid)}
	nextid++
	threads = append(threads, thr)
	go func() {
		defer func() {
			goErr = recover()
			if goErr != nil {
				// Return to the Run thread.
				if debug {
					fmt.Printf("%v panicked: %v\n", thr, goErr)
				}
				runThread.wake <- true
				return
			}
			// Remove this thread.
			if debug {
				fmt.Printf("%v exited\n", thr)
			}
			for i, thr2 := range threads {
				if thr == thr2 {
					copy(threads[i:], threads[i+1:])
					threads = threads[:len(threads)-1]
					close(thr.wake)
					Sched()
					return
				}
			}
			panic("thread not found in threads")
		}()
		if debug {
			fmt.Printf("%v started\n", thr)
		}
		<-thr.wake
		f()
	}()
	Sched()
}

func Sched() {
	if Monitor != nil {
		Monitor()
	}

	this := curThread
	// TODO: Instead of exposing amb.IsReplay, the redundant state
	// checking should be done in amb, and this should provide a
	// state function to amb that wraps up both the user-provided
	// state function and the thread PCs.
	//
	// XXX This whole state thing is wrong. There's something
	// algorithmically wrong, since if I add null state to the
	// basic interleaving test, it doesn't find all of the
	// interleavings. But it's also wrong because it doesn't track
	// *local* state (local variables).
	//
	// Instead, there should probably be a function for recording
	// a frame that takes a list of pointers to local variables.
	// The state at the amb point is then the current call stacks
	// of all threads and the values of all variables in all of
	// those frames, plus globals. It should return something with
	// a "Done" method that you can defer to exit the frame that
	// unregisters its variables. Perhaps there should be a
	// similar function for registering a set of pointers to
	// globals to track.
	//
	// There could even be a source-to-source transform that
	// inserts these, perhaps as part of the standard test
	// infrastructure.
	if State != nil && !amb.IsReplay() {
		// TODO: Actually want whole call stack, but then I
		// have to sort them.
		var pcs [1]uintptr
		runtime.Callers(0, pcs[:])
		this.schedPC = pcs[0]

		// Check for redundant state.
		state := State()
		allPCs := allPCs[:0]
		for _, thr := range threads {
			allPCs = append(allPCs, int(thr.schedPC))
		}
		// TODO: Use uintptr.
		sort.Ints(allPCs)
		pcnode := &rootPCMap
		for _, pc := range allPCs {
			nextPCMap := pcnode.next[uintptr(pc)]
			if nextPCMap == nil {
				if pcnode.next == nil {
					pcnode.next = make(map[uintptr]*pcmap)
				}
				nextPCMap = new(pcmap)
				pcnode.next[uintptr(pc)] = nextPCMap
			}
			pcnode = nextPCMap
		}
		if _, ok := pcnode.states[state]; ok {
			fmt.Printf("redundant state %v/%v\n", allPCs, state)
			// TODO: Clean up goroutines.
			runThread.wake <- true
			select {}
		} else {
			if pcnode.states == nil {
				pcnode.states = make(map[interface{}]struct{})
			}
			pcnode.states[state] = struct{}{}
		}
	}

	// Pick a thread to run next.
	if len(threads) == 0 {
		// The last thread exited. Return to the Run thread.
		runThread.wake <- true
		return
	}
	curThread = threads[amb.Amb(len(threads))]

	if debug {
		fmt.Printf("scheduling %v from %v\n", curThread, threads)
	}

	// Switch to that thread.
	if this == curThread {
		return
	}
	curThread.wake <- true
	<-this.wake
}
