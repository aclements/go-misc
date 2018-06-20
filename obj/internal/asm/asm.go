// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asm

import "sort"

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

// A Loc is a storage location. All Locs are distinct, but may
// represent just part of a location (for example, for an array, the
// Loc would be the whole array, so a write to that Loc may or may not
// alias a read from that Loc).
//
// TODO: It would be nice if we could at least express an aliasing
// hierarchy and have an "Aliases(o Loc) Alias" method. However, for
// SSA we'd probably need to collapse all of the may-alias clusters
// into one location.
type Loc interface {
	is(Loc)
	less(Loc) bool

	String() string

	// IsPartial returns true if this may-alias with other
	// instances of the same location. If this must-alias with the
	// same location, IsPartial returns false.
	IsPartial() bool
}

type locMem struct{}

func (m locMem) is(Loc)          {}
func (m locMem) less(o Loc) bool { return o != m } // locMem comes first
func (m locMem) String() string  { return "mem" }
func (m locMem) IsPartial() bool {
	return true
}

// LocMem represents all of memory that can be proven not to alias
// with other memory locations.
var LocMem = locMem{}

// LocSet is a set of locations.
type LocSet map[Loc]struct{}

// Add adds l to s.
func (s *LocSet) Add(l Loc) {
	(*s)[l] = struct{}{}
}

// Has returns true if l is in s.
func (s *LocSet) Has(l Loc) bool {
	_, ok := (*s)[l]
	return ok
}

// Ordered returns the set of locations in s in deterministic order.
func (s *LocSet) Ordered() []Loc {
	locs := make([]Loc, 0, len(*s))
	for loc := range *s {
		locs = append(locs, loc)
	}
	sort.Slice(locs, func(i, j int) bool { return locs[i].less(locs[j]) })
	return locs
}
