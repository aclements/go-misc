// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"io"
	"log"

	"golang.org/x/tools/go/ssa"
)

// ValState tracks the known dynamic values of instructions and heap
// objects.
type ValState struct {
	frame *frameValState
	heap  *heapValState
}

// frameValState tracks the known dynamic values of ssa.Values in a
// particular execution path of a single stack frame.
//
// frameValState is effectively a persistent map represented as a
// linked list possibly terminated in a Go map. For efficiency, the
// linked list is compacted into a Go map when the size of the linked
// list and the size of the map at the end of the chain are the same.
type frameValState struct {
	parent *frameValState
	budget int

	// If flat is non-nil, parent, bind, and val must all be nil.
	flat map[ssa.Instruction]DynValue

	bind ssa.Instruction // Must also be ssa.Value
	val  DynValue        // nil to unbind this instruction
}

// heapValState tracks the known dynamic values of heap objects.
type heapValState struct {
	parent *heapValState
	budget int

	// If flat is non-nil, parent, bind, and val must all be nil.
	flat map[*HeapObject]DynValue

	bind *HeapObject // A value in the heap
	val  DynValue    // nil to unbind this object
}

// Get returns the dynamic value of val, or nil if unknown. val may be
// a pure ssa.Value (not an ssa.Instruction), in which case it will be
// resolved directly to a DynValue if possible. Otherwise, Get will
// look up the value bound to val by a previous call to Extend.
func (vs ValState) Get(val ssa.Value) DynValue {
	switch val := val.(type) {
	case *ssa.Const:
		if val.Value == nil {
			return DynNil{}
		}
		return DynConst{val.Value}
	case *ssa.Global:
		return DynGlobal{val}
	}
	instr, ok := val.(ssa.Instruction)
	if !ok {
		return nil
	}
	for frame := vs.frame; frame != nil; frame = frame.parent {
		if frame.flat != nil {
			return frame.flat[instr]
		}
		if frame.bind == instr {
			return frame.val
		}
	}
	return nil
}

// GetHeap returns the dynamic value of a heap object, or nil if
// unknown.
func (vs ValState) GetHeap(h *HeapObject) DynValue {
	for heap := vs.heap; heap != nil; heap = heap.parent {
		if heap.flat != nil {
			return heap.flat[h]
		}
		if heap.bind == h {
			return heap.val
		}
	}
	return nil
}

// Extend returns a new ValState that is like vs, but with bind bound
// to dynamic value val. If dyn is dynUnknown, Extend unbinds val.
// Extend is a no-op if called with a pure ssa.Value.
func (vs ValState) Extend(val ssa.Value, dyn DynValue) ValState {
	if _, ok := dyn.(dynUnknown); ok {
		// "Unbind" val.
		if vs.Get(val) == nil {
			return vs
		}
		dyn = nil
	}
	// We only care about binding instruction values.
	instr, ok := val.(ssa.Instruction)
	if !ok {
		return vs
	}

	budget := 4
	if vs.frame != nil {
		budget = vs.frame.budget - 1
	}
	vs = ValState{&frameValState{vs.frame, budget, nil, instr, dyn}, vs.heap}
	if vs.frame.budget <= 0 {
		vs.frame.flatten()
	}
	return vs
}

// ExtendHeap returns a new ValState that is like vs, but with heap
// object h bound to dynamic value val.
func (vs ValState) ExtendHeap(h *HeapObject, dyn DynValue) ValState {
	if _, ok := dyn.(dynUnknown); ok {
		// "Unbind" val.
		if vs.GetHeap(h) == nil {
			return vs
		}
		dyn = nil
	}

	budget := 4
	if vs.heap != nil {
		budget = vs.heap.budget - 1
	}
	vs = ValState{vs.frame, &heapValState{vs.heap, budget, nil, h, dyn}}
	if vs.heap.budget <= 0 {
		vs.heap.flatten()
	}
	return vs
}

// LimitToHeap returns a ValState containing only the heap bindings in
// vs.
func (vs ValState) LimitToHeap() ValState {
	return ValState{nil, vs.heap}
}

// Do applies the effect of instr to the value state and returns an
// Extended ValState.
func (vs ValState) Do(instr ssa.Instruction) ValState {
	switch instr := instr.(type) {
	case *ssa.BinOp:
		if x, y := vs.Get(instr.X), vs.Get(instr.Y); x != nil && y != nil {
			return vs.Extend(instr, x.BinOp(instr.Op, y))
		}

	case *ssa.UnOp:
		if x := vs.Get(instr.X); x != nil {
			return vs.Extend(instr, x.UnOp(instr.Op, vs))
		}

	case *ssa.ChangeType:
		if x := vs.Get(instr.X); x != nil {
			return vs.Extend(instr, x)
		}

	case *ssa.FieldAddr:
		if x := vs.Get(instr.X); x != nil {
			switch x := x.(type) {
			case DynGlobal:
				return vs.Extend(instr, DynFieldAddr{x.global, instr.Field})
			case DynHeapPtr:
				return vs.Extend(instr, x.FieldAddr(vs, instr))
			}
		}

	case *ssa.Store:
		// Handle stores to tracked heap objects.
		//
		// TODO: This could be storing to something in the
		// known heap, but we may have failed to track the
		// aliasing of it and think that this is untracked.
		if addr := vs.Get(instr.Addr); addr != nil {
			if addr, ok := addr.(DynHeapPtr); ok {
				val := vs.Get(instr.Val)
				if val == nil {
					val = dynUnknown{}
				}
				return vs.ExtendHeap(addr.elem, val)
			}
		}

		// TODO: ssa.Convert, ssa.Field
	}
	return vs
}

func (fs *frameValState) flatten() map[ssa.Instruction]DynValue {
	if fs == nil {
		return nil
	}
	if fs.flat != nil {
		return fs.flat
	}
	// Collect bindings into a map.
	flat := make(map[ssa.Instruction]DynValue)
	for fs2 := fs; fs2 != nil; fs2 = fs2.parent {
		if fs2.flat != nil {
			for k, v := range fs2.flat {
				if _, ok := flat[k]; !ok {
					flat[k] = v
				}
			}
			break
		}
		if _, ok := flat[fs2.bind]; !ok {
			flat[fs2.bind] = fs2.val
		}
	}
	// Eliminate unbound values.
	for k, v := range flat {
		if v == nil {
			delete(flat, k)
		}
	}
	fs.flat = flat
	fs.budget = len(flat) + 1
	fs.parent, fs.bind, fs.val = nil, nil, nil
	return fs.flat
}

func (hs *heapValState) flatten() map[*HeapObject]DynValue {
	if hs == nil {
		return nil
	}
	if hs.flat != nil {
		return hs.flat
	}
	// Collect bindings into a map.
	flat := make(map[*HeapObject]DynValue)
	for hs2 := hs; hs2 != nil; hs2 = hs2.parent {
		if hs2.flat != nil {
			for k, v := range hs2.flat {
				if _, ok := flat[k]; !ok {
					flat[k] = v
				}
			}
			break
		}
		if _, ok := flat[hs2.bind]; !ok {
			flat[hs2.bind] = hs2.val
		}
	}
	// Eliminate unbound values.
	for k, v := range flat {
		if v == nil {
			delete(flat, k)
		}
	}
	hs.flat = flat
	hs.budget = len(flat) + 1
	hs.parent, hs.bind, hs.val = nil, nil, nil
	return hs.flat
}

// EqualAt returns true if vs and o have equal dynamic values for each
// value in at, and equal heap values for all heap objects.
func (vs ValState) EqualAt(o ValState, at map[ssa.Instruction]struct{}) bool {
	if len(at) != 0 {
		// Check frame state.
		i1, i2 := vs.frame.flatten(), o.frame.flatten()
		for k := range at {
			v1, ok1 := i1[k]
			v2, ok2 := i2[k]
			if ok1 != ok2 || (ok1 && !v1.Equal(v2)) {
				return false
			}
		}
	}
	// Check heap state.
	h1, h2 := vs.heap.flatten(), o.heap.flatten()
	if len(h1) != len(h2) {
		return false
	}
	for k1, v1 := range h1 {
		if v2, ok := h2[k1]; !ok || !v1.Equal(v2) {
			return false
		}
	}
	return true
}

// WriteTo writes a debug representation of vs to w.
func (vs ValState) WriteTo(w io.Writer) {
	// TODO: Sort.
	h := vs.heap.flatten()
	for bind, val := range h {
		fmt.Fprintf(w, "%s = %v\n", bind, val)
	}
	f := vs.frame.flatten()
	for bind, val := range f {
		fmt.Fprintf(w, "%s = %v\n", bind.(ssa.Value).Name(), val)
	}
}

// A DynValue is the dynamic value of an ssa.Value on a particular
// execution path. It can track any scalar value and addresses that
// cannot alias (e.g., addresses of globals).
type DynValue interface {
	Equal(other DynValue) bool
	BinOp(op token.Token, other DynValue) DynValue
	UnOp(op token.Token, vs ValState) DynValue
}

type dynUnknown struct{}

func (dynUnknown) Equal(other DynValue) bool {
	panic("Equal on unknown dynamic value")
}

func (dynUnknown) BinOp(op token.Token, other DynValue) DynValue {
	panic("BinOp on unknown dynamic value")
}

func (dynUnknown) UnOp(op token.Token, vs ValState) DynValue {
	panic("UnOp on unknown dynamic value")
}

// BUG: DynConst is infinite precision. It should track its type and
// truncate the results of every operation.

type DynConst struct {
	c constant.Value
}

func (x DynConst) Equal(y DynValue) bool {
	return constant.Compare(x.c, token.EQL, y.(DynConst).c)
}

func (x DynConst) BinOp(op token.Token, y DynValue) DynValue {
	yc := y.(DynConst).c
	switch op {
	case token.EQL, token.NEQ,
		token.LSS, token.LEQ,
		token.GTR, token.GEQ:
		// Bleh. constant.BinaryOp doesn't work on comparison
		// operations.
		result := constant.Compare(x.c, op, yc)
		return DynConst{constant.MakeBool(result)}
	case token.SHL, token.SHR:
		s, exact := constant.Uint64Val(yc)
		if !exact {
			log.Fatalf("bad shift %v", y)
		}
		return DynConst{constant.Shift(x.c, op, uint(s))}
	default:
		return DynConst{constant.BinaryOp(x.c, op, yc)}
	}
}

func (x DynConst) UnOp(op token.Token, vs ValState) DynValue {
	return DynConst{constant.UnaryOp(op, x.c, 64)}
}

// comparableBinOp implements DynValue.BinOp for values that support
// only comparison operators.
func comparableBinOp(x DynValue, op token.Token, y DynValue) DynValue {
	equal := x.Equal(y)
	switch op {
	case token.EQL:
		return DynConst{constant.MakeBool(equal)}
	case token.NEQ:
		return DynConst{constant.MakeBool(!equal)}
	}
	log.Fatalf("bad pointer operation: %v", op)
	panic("unreachable")
}

func addrUnOp(op token.Token) DynValue {
	switch op {
	case token.MUL:
		return dynUnknown{}
	}
	log.Fatalf("bad pointer operation: %v", op)
	panic("unreachable")
}

// DynNil is a nil pointer.
type DynNil struct{}

func (x DynNil) Equal(y DynValue) bool {
	_, isNil := y.(DynNil)
	return isNil
}

func (x DynNil) BinOp(op token.Token, y DynValue) DynValue {
	return comparableBinOp(x, op, y)
}

func (x DynNil) UnOp(op token.Token, vs ValState) DynValue {
	return addrUnOp(op)
}

// DynGlobal is the address of a global. Because it's the address of a
// global, it can only alias other DynGlobals.
type DynGlobal struct {
	global *ssa.Global
}

func (x DynGlobal) Equal(y DynValue) bool {
	yg, isGlobal := y.(DynGlobal)
	return isGlobal && x.global == yg.global
}

func (x DynGlobal) BinOp(op token.Token, y DynValue) DynValue {
	return comparableBinOp(x, op, y)
}

func (x DynGlobal) UnOp(op token.Token, vs ValState) DynValue {
	return addrUnOp(op)
}

// DynFieldAddr is the address of a field in a global. Because it is
// only fields in globals, it can only alias other DynFieldAddrs.
//
// TODO: We could unify DynFieldAddr and DynHeapAddr if we created
// (and cached) HeapObjects for globals and fields of globals as
// needed.
type DynFieldAddr struct {
	object *ssa.Global
	field  int
}

func (x DynFieldAddr) Equal(y DynValue) bool {
	y2, isFieldAddr := y.(DynFieldAddr)
	return isFieldAddr && x.object == y2.object && x.field == y2.field
}

func (x DynFieldAddr) BinOp(op token.Token, y DynValue) DynValue {
	return comparableBinOp(x, op, y)
}

func (x DynFieldAddr) UnOp(op token.Token, vs ValState) DynValue {
	return addrUnOp(op)
}

// DynHeapPtr is a pointer to a tracked heap object. Because globals
// and heap objects are tracked separately, a DynHeapPtr can only
// alias other DynHeapPtrs.
type DynHeapPtr struct {
	elem *HeapObject
}

func (x DynHeapPtr) String() string {
	return "&" + x.elem.String()
}

func (x DynHeapPtr) Equal(y DynValue) bool {
	y2, isHeapPtr := y.(DynHeapPtr)
	return isHeapPtr && x.elem == y2.elem
}

func (x DynHeapPtr) BinOp(op token.Token, y DynValue) DynValue {
	return comparableBinOp(x, op, y)
}

func (x DynHeapPtr) UnOp(op token.Token, vs ValState) DynValue {
	if op == token.MUL {
		return vs.GetHeap(x.elem)
	}
	return addrUnOp(op)
}

func (x DynHeapPtr) FieldAddr(vs ValState, instr *ssa.FieldAddr) DynValue {
	obj := vs.GetHeap(x.elem)
	if obj == nil {
		return dynUnknown{}
	}
	strct := obj.(DynStruct)
	fieldName := instr.X.Type().(*types.Pointer).Elem().Underlying().(*types.Struct).Field(instr.Field).Name()
	if fieldVal, ok := strct[fieldName]; ok {
		return DynHeapPtr{fieldVal}
	}
	return dynUnknown{}
}

// DynStruct is a struct value consisting of heap objects. It maps
// from field name to heap object. Note that each tracked field is its
// own heap object; e.g., even if it's just an int field, it's
// considered a HeapObject. This makes it possible to track pointers
// to fields.
type DynStruct map[string]*HeapObject

func (x DynStruct) Equal(y DynValue) bool {
	y2, ok := y.(DynStruct)
	if !ok || len(x) != len(y2) {
		return false
	}
	for k, v := range x {
		if y2[k] != v {
			return false
		}
	}
	return true
}

func (x DynStruct) BinOp(op token.Token, y DynValue) DynValue {
	return comparableBinOp(x, op, y)
}

func (x DynStruct) UnOp(op token.Token, vs ValState) DynValue {
	log.Fatal("bad struct operation: %v", op)
	panic("unreachable")
}

// A HeapObject is a tracked object in the heap. HeapObjects have
// identity; that is, for two *HeapObjects x and y, they refer to the
// same heap object if and only if x == y. HeapObjects have a string
// label for debugging purposes, but this label does not affect
// identity.
type HeapObject struct {
	label string
}

func NewHeapObject(label string) *HeapObject {
	return &HeapObject{label}
}

func (h *HeapObject) String() string {
	return "heap:" + h.label
}
