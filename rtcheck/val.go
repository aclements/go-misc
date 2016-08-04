package main

import (
	"fmt"
	"go/constant"
	"go/token"
	"io"
	"log"

	"golang.org/x/tools/go/ssa"
)

// ValState tracks the known dynamic values of ssa.Values in a
// particular execution path.
type ValState struct {
	parent *ValState

	bind ssa.Instruction // Must also be ssa.Value
	val  DynValue
}

func (vs *ValState) Get(val ssa.Value) DynValue {
	switch val := val.(type) {
	case *ssa.Const:
		if val.Value == nil {
			return DynNil{}
		}
		return DynConst{val.Value}
	case *ssa.Global:
		return DynGlobal{val}
	case *ssa.FieldAddr:
		if x := vs.Get(val.X); x != nil {
			return DynFieldAddr{x, val.Field}
		}
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

func (vs *ValState) Extend(bind ssa.Value, val DynValue) *ValState {
	if _, ok := val.(dynUnknown); ok {
		return vs
	}
	// We only care about binding instruction values.
	instr, ok := bind.(ssa.Instruction)
	if !ok {
		return vs
	}
	return &ValState{vs, instr, val}
}

func (vs *ValState) Do(instr ssa.Instruction) *ValState {
	switch instr := instr.(type) {
	case *ssa.BinOp:
		if x, y := vs.Get(instr.X), vs.Get(instr.Y); x != nil && y != nil {
			vs = vs.Extend(instr, x.BinOp(instr.Op, y))
		}

	case *ssa.UnOp:
		if x := vs.Get(instr.X); x != nil {
			vs = vs.Extend(instr, x.UnOp(instr.Op))
		}

	case *ssa.ChangeType:
		if x := vs.Get(instr.X); x != nil {
			vs = vs.Extend(instr, x)
		}

		// TODO: ssa.Convert
	}
	return vs
}

func (vs *ValState) EqualAt(o *ValState, at map[ssa.Instruction]struct{}) bool {
	if len(at) == 0 {
		// Fast path for empty at set.
		return true
	}
	flatten := func(vs *ValState) map[ssa.Instruction]DynValue {
		// TODO: Cache flattening?
		out := make(map[ssa.Instruction]DynValue)
		for ; vs != nil; vs = vs.parent {
			if _, keep := at[vs.bind]; !keep {
				continue
			}
			if _, ok := out[vs.bind]; !ok {
				out[vs.bind] = vs.val
			}
		}
		return out
	}
	vs1, vs2 := flatten(vs), flatten(o)
	if len(vs1) != len(vs2) {
		return false
	}
	for k1, v1 := range vs1 {
		if v2, ok := vs2[k1]; !ok || !v1.Equal(v2) {
			return false
		}
	}
	return true
}

func (vs *ValState) WriteTo(w io.Writer) {
	shown := map[ssa.Instruction]struct{}{}
	for ; vs != nil; vs = vs.parent {
		if _, ok := shown[vs.bind]; ok {
			continue
		}
		fmt.Fprintf(w, "%v = %v\n", vs.bind.(ssa.Value).Name(), vs.val)
		shown[vs.bind] = struct{}{}
	}
}

// A DynValue is the dynamic value of an ssa.Value on a particular
// execution path. It can track any scalar value and addresses that
// cannot alias (e.g., addresses of globals).
type DynValue interface {
	Equal(other DynValue) bool
	BinOp(op token.Token, other DynValue) DynValue
	UnOp(op token.Token) DynValue
}

type dynUnknown struct{}

func (dynUnknown) Equal(other DynValue) bool {
	panic("Equal on unknown dynamic value")
}

func (dynUnknown) BinOp(op token.Token, other DynValue) DynValue {
	panic("BinOp on unknown dynamic value")
}

func (dynUnknown) UnOp(op token.Token) DynValue {
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

func (x DynConst) UnOp(op token.Token) DynValue {
	return DynConst{constant.UnaryOp(op, x.c, 64)}
}

func addrBinOp(x DynValue, op token.Token, y DynValue) DynValue {
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

type DynNil struct{}

func (x DynNil) Equal(y DynValue) bool {
	_, isNil := y.(DynNil)
	return isNil
}

func (x DynNil) BinOp(op token.Token, y DynValue) DynValue {
	return addrBinOp(x, op, y)
}

func (x DynNil) UnOp(op token.Token) DynValue {
	return addrUnOp(op)
}

type DynGlobal struct {
	global *ssa.Global
}

func (x DynGlobal) Equal(y DynValue) bool {
	yg, isGlobal := y.(DynGlobal)
	return isGlobal && x.global == yg.global
}

func (x DynGlobal) BinOp(op token.Token, y DynValue) DynValue {
	return addrBinOp(x, op, y)
}

func (x DynGlobal) UnOp(op token.Token) DynValue {
	return addrUnOp(op)
}

type DynFieldAddr struct {
	object DynValue // Must be address-like
	field  int
}

func (x DynFieldAddr) Equal(y DynValue) bool {
	yfa, isFieldAddr := y.(DynFieldAddr)
	return isFieldAddr && x.field == yfa.field && x.object.Equal(yfa.object)
}

func (x DynFieldAddr) BinOp(op token.Token, y DynValue) DynValue {
	return addrBinOp(x, op, y)
}

func (x DynFieldAddr) UnOp(op token.Token) DynValue {
	return addrUnOp(op)
}
