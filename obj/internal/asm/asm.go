// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asm

import "math/bits"

// TODO: Generalize to more than an index so we can support stack
// slots. Compute stack slot Locs.

// Seq is a sequence of instructions.
type Seq interface {
	Len() int
	Get(i int) Inst
}

// Inst is a single machine instruction.
type Inst interface {
	// GoSyntax returns the Go assembler syntax representation of
	// this instruction. symname, if non-nil, must return the name
	// and base of the symbol containing address addr, or "" if
	// symbol lookup fails.
	GoSyntax(symname func(addr uint64) (string, uint64)) string

	// PC returns the address of this instruction.
	PC() uint64

	// Control returns the control-flow effects of this
	// instruction.
	Control() Control

	// Effects returns the read and write sets of this
	// instruction.
	Effects() (read, write LocSet)
}

// Arg is an argument to an instruction.
type Arg interface {
}

// Control captures control-flow effects of an instruction.
type Control struct {
	Type        ControlType
	Conditional bool
	TargetPC    uint64
	Target      Arg
}

type ControlType uint8

const (
	ControlNone ControlType = iota
	ControlJump
	ControlCall
	ControlRet

	// ControlExit is like a call that never returns.
	ControlExit
)

type LocSet uint64

type Loc uint8

const (
	LocMem Loc = iota
	locArch
)

func (s LocSet) Aliases(o LocSet) Alias {
	alias := AliasNo
	if s&o&(1<<LocMem) != 0 {
		// Memory aliases are imprecise.
		alias = AliasMay
	}
	if (s&o)&^(1<<LocMem) != 0 {
		// Register aliases are precise.
		//
		// TODO: Not strictly true because we coarsen, e.g.,
		// AL and AH.
		alias = AliasMust
	}
	return alias
}

type Alias int

const (
	AliasNo Alias = iota
	AliasMay
	AliasMust
)

func (s LocSet) First() (Loc, bool) {
	if s == 0 {
		return 0, false
	}
	return Loc(bits.TrailingZeros64(uint64(s))), true
}

func (s LocSet) Next(n Loc) (Loc, bool) {
	s >>= uint(n + 1)
	if s == 0 {
		return 0, false
	}
	return n + 1 + Loc(bits.TrailingZeros64(uint64(s))), true
}
