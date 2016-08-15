// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"go/constant"
	"log"

	"golang.org/x/tools/go/ssa"
)

// TODO: Stack barrier locks, semaphores, etc.

// A callHandler implements special handling of a function call. It
// should append the updated PathState to newps and return the
// resulting slice.
type callHandler func(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState

// callHandlers maps from function names (the result of
// ssa.Function.String()) to handlers for special functions.
//
// TODO: Several of these are not nosplit and hence should model the
// implicit morestack.
var callHandlers map[string]callHandler

func init() {
	// Go's initialization order rule doesn't distinguish between
	// function pointers and function calls, so we have to
	// initialize callHandlers outside of the initialization order.
	callHandlers = map[string]callHandler{
		"runtime.lock":   handleRuntimeLock,
		"runtime.unlock": handleRuntimeUnlock,

		"runtime.casgstatus":          handleRuntimeCasgstatus,
		"runtime.castogscanstatus":    handleRuntimeCastogscanstatus,
		"runtime.casfrom_Gscanstatus": handleRuntimeCasfrom_Gscanstatus,

		"runtime.getg":                    handleRuntimeGetg,
		"runtime.acquirem":                handleRuntimeAcquirem,
		"runtime.rtcheck۰presystemstack":  handleRuntimePresystemstack,
		"runtime.rtcheck۰postsystemstack": handleRuntimePostsystemstack,

		"runtime.morestack": handleRuntimeMorestack,

		// restartg does a conditional unlock of _Gscan, but it's hard
		// to track that condition. In practice, it always does the
		// unlock, so handle it just like casefrom_Gscanstatus.
		//
		// TODO: This function is silly. We should probably remove it
		// from the runtime.
		"runtime.restartg": handleRuntimeCasfrom_Gscanstatus,
	}
}

func handleRuntimeLock(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	// TODO: Most of the lock(&mheap_.lock) have an empty
	// points-to set, which means we can't track that lock.
	lock := s.pta.Queries[instr.(*ssa.Call).Call.Args[0]].PointsTo()
	newls := NewLockSet(ps.lockSet.sp).Plus(lock, s.stack)
	s.lockOrder.Add(ps.lockSet, newls, s.stack)
	ls2 := ps.lockSet.Plus(lock, s.stack)
	// If we self-deadlocked, terminate this path.
	//
	// TODO: This is only sound if we know it's the same lock
	// *instance*.
	if ps.lockSet == ls2 && len(lock.Labels()) > 0 {
		s.warnp(instr.Pos(), "possible self-deadlock %s %s; trimming path", ps.lockSet, lock)
		return newps
	}
	ps.lockSet = ls2
	return append(newps, ps)
}

func handleRuntimeUnlock(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	lock := s.pta.Queries[instr.(*ssa.Call).Call.Args[0]].PointsTo()
	// TODO: Warn on unlock of unlocked lock.
	ps.lockSet = ps.lockSet.Minus(lock)
	return append(newps, ps)
}

func handleRuntimeCasgstatus(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	// Equivalent to acquiring and releasing _Gscan.
	gscan := NewLockSet(ps.lockSet.sp).PlusLabel("_Gscan", s.stack)
	s.lockOrder.Add(ps.lockSet, gscan, s.stack)
	return append(newps, ps)
}

func handleRuntimeCastogscanstatus(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	// This is a conditional acquisition of _Gscan. _Gscan is
	// acquired on the true branch and not acquired on the false
	// branch. Either way it participates in the lock order.
	gscan := NewLockSet(ps.lockSet.sp).PlusLabel("_Gscan", s.stack)
	s.lockOrder.Add(ps.lockSet, gscan, s.stack)

	psT, psF := ps, ps

	psT.lockSet = psT.lockSet.PlusLabel("_Gscan", s.stack)
	psT.vs = psT.vs.Extend(instr.(ssa.Value), DynConst{constant.MakeBool(true)})

	psF.vs = psF.vs.Extend(instr.(ssa.Value), DynConst{constant.MakeBool(false)})

	return append(newps, psT, psF)
}

func handleRuntimeCasfrom_Gscanstatus(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	// Unlock of _Gscan.
	ps.lockSet = ps.lockSet.MinusLabel("_Gscan")
	return append(newps, ps)
}

func handleRuntimeGetg(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	val := ps.vs.GetHeap(s.heap.curG)
	if val == nil {
		log.Fatal("failed to determine current G")
	}
	ps.vs = ps.vs.Extend(instr.(ssa.Value), val)
	return append(newps, ps)
}

func handleRuntimeAcquirem(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	// TODO: Update m.locks.
	ps.vs = ps.vs.Extend(instr.(ssa.Value), DynHeapPtr{s.heap.curM})
	return append(newps, ps)
}

func handleRuntimePresystemstack(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	// Get the current G.
	curG := ps.vs.GetHeap(s.heap.curG)
	if curG == nil {
		log.Fatal("failed to determine current G")
	}
	// Set the current G to g0. This is a no-op if we're already
	// on the system stack.
	ps.vs = ps.vs.ExtendHeap(s.heap.curG, DynHeapPtr{s.heap.g0})
	// Return the original G.
	ps.vs = ps.vs.Extend(instr.(ssa.Value), curG)
	return append(newps, ps)
}

func handleRuntimePostsystemstack(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	// Return the to g returned by presystemstack.
	origG := ps.vs.Get(instr.(*ssa.Call).Call.Args[0])
	if origG == nil {
		log.Fatal("failed to restore G returned by presystemstack")
	}
	ps.vs = ps.vs.ExtendHeap(s.heap.curG, origG)
	return append(newps, ps)
}

func handleRuntimeMorestack(s *state, ps PathState, instr ssa.Instruction, newps []PathState) []PathState {
	// Get the current G.
	curG := ps.vs.GetHeap(s.heap.curG)
	if curG == nil {
		log.Fatal("failed to determine current G")
	}
	// If we're on the system stack we either have room or we
	// panic, so just return from morestack.
	if curG.(DynHeapPtr).elem == s.heap.g0 {
		return append(newps, ps)
	}

	// Otherwise, we may or may not call newstack. Take both
	// paths. This is important because newstack doesn't
	// technically "return", so we're going to lose that execution
	// path.
	newps = append(newps, ps)

	// Call newstack on the system stack.
	//
	// TODO: This duplicates some of doCall. Can I make the
	// walkFunction API nicer so this is nicer?
	newstack := instr.Parent().Prog.ImportedPackage("runtime").Func("newstack")
	ps = PathState{
		lockSet: ps.lockSet,
		vs:      ps.vs.ExtendHeap(s.heap.curG, DynHeapPtr{s.heap.g0}).LimitToHeap(),
	}
	s.walkFunction(newstack, ps).ForEach(func(ps2 PathState) {
		ps.lockSet = ps2.lockSet
		ps.vs.heap = ps2.vs.heap
		// Leave system stack.
		ps.vs = ps.vs.ExtendHeap(s.heap.curG, curG)
		newps = append(newps, ps)
	})
	return newps
}
