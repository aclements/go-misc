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

// ValState tracks the known dynamic values of ssa.Values in a
// particular execution path.
type ValState struct {
	parent *ValState

	// Either bind or bindh must be set.
	bind  ssa.Instruction // Must also be ssa.Value
	bindh *HeapObject     // A value in the heap
	val   DynValue        // nil to unbind this instruction/object
}

// Get returns the dynamic value of val, or nil if unknown. val may be
// a pure ssa.Value (not an ssa.Instruction), in which case it will be
// resolved directly to a DynValue if possible. Otherwise, Get will
// look up the value bound to val by a previous call to Extend.
func (vs *ValState) Get(val ssa.Value) DynValue {
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
	for vs != nil {
		if vs.bind == instr {
			return vs.val
		}
		vs = vs.parent
	}
	return nil
}

// GetHeap returns the dynamic value of a heap object, or nil if
// unknown.
func (vs *ValState) GetHeap(h *HeapObject) DynValue {
	for vs != nil {
		if vs.bindh == h {
			return vs.val
		}
		vs = vs.parent
	}
	return nil
}

// Extend returns a new ValState that is like vs, but with bind bound
// to dynamic value val. If dyn is dynUnknown, Extend unbinds val.
// Extend is a no-op if called with a pure ssa.Value.
func (vs *ValState) Extend(val ssa.Value, dyn DynValue) *ValState {
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
	return &ValState{vs, instr, nil, dyn}
}

// ExtendHeap returns a new ValState that is like vs, but with heap
// object h bound to dynamic value val.
func (vs *ValState) ExtendHeap(h *HeapObject, dyn DynValue) *ValState {
	if _, ok := dyn.(dynUnknown); ok {
		// "Unbind" val.
		if vs.GetHeap(h) == nil {
			return vs
		}
		dyn = nil
	}
	return &ValState{vs, nil, h, dyn}
}

// LimitToHeap returns a ValState containing only the heap bindings in
// vs.
func (vs *ValState) LimitToHeap() *ValState {
	var newvs *ValState
	have := make(map[*HeapObject]struct{})
	for ; vs != nil; vs = vs.parent {
		if vs.bindh == nil {
			continue
		}
		if _, ok := have[vs.bindh]; ok {
			continue
		}
		have[vs.bindh] = struct{}{}
		if vs.val != nil {
			newvs = newvs.ExtendHeap(vs.bindh, vs.val)
		}
	}
	return newvs
}

// Do applies the effect of instr to the value state and returns an
// Extended ValState.
func (vs *ValState) Do(instr ssa.Instruction) *ValState {
	switch instr := instr.(type) {
	case *ssa.BinOp:
		if x, y := vs.Get(instr.X), vs.Get(instr.Y); x != nil && y != nil {
			vs = vs.Extend(instr, x.BinOp(instr.Op, y))
		}

	case *ssa.UnOp:
		if x := vs.Get(instr.X); x != nil {
			vs = vs.Extend(instr, x.UnOp(instr.Op, vs))
		}

	case *ssa.ChangeType:
		if x := vs.Get(instr.X); x != nil {
			vs = vs.Extend(instr, x)
		}

	case *ssa.FieldAddr:
		if x := vs.Get(instr.X); x != nil {
			switch x := x.(type) {
			case DynGlobal:
				vs = vs.Extend(instr, DynFieldAddr{x.global, instr.Field})
			case DynHeapPtr:
				vs = vs.Extend(instr, x.FieldAddr(vs, instr))
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
				vs = vs.ExtendHeap(addr.elem, val)
			}
		}

		// TODO: ssa.Convert, ssa.Field
	}
	return vs
}

// EqualAt returns true if vs and o have equal dynamic values for each
// value in at, and equal heap values for all heap objects.
func (vs *ValState) EqualAt(o *ValState, at map[ssa.Instruction]struct{}) bool {
	if len(at) == 0 {
		// Fast path for empty at set.
		return true
	}
	flatten := func(vs *ValState) (map[ssa.Instruction]DynValue, map[*HeapObject]DynValue) {
		// TODO: Cache flattening?
		instrs := make(map[ssa.Instruction]DynValue)
		heap := make(map[*HeapObject]DynValue)
		for ; vs != nil; vs = vs.parent {
			if vs.bindh != nil {
				if _, ok := heap[vs.bindh]; !ok {
					heap[vs.bindh] = vs.val
				}
			} else {
				if _, keep := at[vs.bind]; !keep {
					continue
				}
				if _, ok := instrs[vs.bind]; !ok {
					instrs[vs.bind] = vs.val
				}
			}
		}
		// Eliminate unbound values.
		for k, v := range instrs {
			if v == nil {
				delete(instrs, k)
			}
		}
		for k, v := range heap {
			if v == nil {
				delete(heap, k)
			}
		}
		return instrs, heap
	}
	vs1i, vs1h := flatten(vs)
	vs2i, vs2h := flatten(o)
	if len(vs1i) != len(vs2i) || len(vs1h) != len(vs2h) {
		return false
	}
	for k1, v1 := range vs1i {
		if v2, ok := vs2i[k1]; !ok || !v1.Equal(v2) {
			return false
		}
	}
	for k1, v1 := range vs1h {
		if v2, ok := vs2h[k1]; !ok || !v1.Equal(v2) {
			return false
		}
	}
	return true
}

// WriteTo writes a debug representation of vs to w.
func (vs *ValState) WriteTo(w io.Writer) {
	shown := map[ssa.Instruction]struct{}{}
	shownh := map[*HeapObject]struct{}{}
	for ; vs != nil; vs = vs.parent {
		if vs.bindh != nil {
			if _, ok := shownh[vs.bindh]; ok {
				continue
			}
			shownh[vs.bindh] = struct{}{}
			fmt.Fprintf(w, "%s", vs.bindh)
		} else {
			if _, ok := shown[vs.bind]; ok {
				continue
			}
			shown[vs.bind] = struct{}{}
			fmt.Fprintf(w, "%s", vs.bind.(ssa.Value).Name())
		}
		fmt.Fprintf(w, " = %v\n", vs.val)
	}
}

// A DynValue is the dynamic value of an ssa.Value on a particular
// execution path. It can track any scalar value and addresses that
// cannot alias (e.g., addresses of globals).
type DynValue interface {
	Equal(other DynValue) bool
	BinOp(op token.Token, other DynValue) DynValue
	UnOp(op token.Token, vs *ValState) DynValue
}

type dynUnknown struct{}

func (dynUnknown) Equal(other DynValue) bool {
	panic("Equal on unknown dynamic value")
}

func (dynUnknown) BinOp(op token.Token, other DynValue) DynValue {
	panic("BinOp on unknown dynamic value")
}

func (dynUnknown) UnOp(op token.Token, vs *ValState) DynValue {
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

func (x DynConst) UnOp(op token.Token, vs *ValState) DynValue {
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

func (x DynNil) UnOp(op token.Token, vs *ValState) DynValue {
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

func (x DynGlobal) UnOp(op token.Token, vs *ValState) DynValue {
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

func (x DynFieldAddr) UnOp(op token.Token, vs *ValState) DynValue {
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

func (x DynHeapPtr) UnOp(op token.Token, vs *ValState) DynValue {
	if op == token.MUL {
		return vs.GetHeap(x.elem)
	}
	return addrUnOp(op)
}

func (x DynHeapPtr) FieldAddr(vs *ValState, instr *ssa.FieldAddr) DynValue {
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

func (x DynStruct) UnOp(op token.Token, vs *ValState) DynValue {
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
