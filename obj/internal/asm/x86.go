// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asm

import (
	"fmt"

	"golang.org/x/arch/x86/x86asm"
)

//go:generate sh -c "go run mkx86info.go | gofmt > x86info.go"

type x86Seq []x86Inst

func (s x86Seq) Len() int {
	return len(s)
}

func (s x86Seq) Get(i int) Inst {
	return &s[i]
}

func DisasmX86_64(text []byte, pc uint64) Seq {
	var out x86Seq
	for len(text) > 0 {
		inst, err := x86asm.Decode(text, 64) // TODO: Support 32-bit
		size := inst.Len
		if err != nil || size == 0 || inst.Op == 0 {
			inst = x86asm.Inst{}
		}
		if size == 0 {
			size = 1
		}
		out = append(out, x86Inst{inst, pc})

		text = text[size:]
		pc += uint64(size)
	}
	return out

}

type x86Inst struct {
	x86asm.Inst
	pc uint64
}

func (i *x86Inst) GoSyntax(symname func(uint64) (string, uint64)) string {
	if i.Op == 0 {
		return "?"
	}
	return x86asm.GoSyntax(i.Inst, i.pc, symname)
}

func (i *x86Inst) PC() uint64 {
	return i.pc
}

func (i *x86Inst) Control() Control {
	var c Control

	// Handle REP-prefixed instructions.
	for _, pfx := range i.Inst.Prefix {
		if pfx == 0 {
			break
		}
		if pfx == x86asm.PrefixREP || pfx == x86asm.PrefixREPN {
			c.Type = ControlJump
			c.Conditional = true
			c.TargetPC = i.pc
			return c
		}
	}

	// Handle explicit control flow instructions.
	switch i.Op {
	default:
		return c
	case x86asm.CALL:
		c.Type = ControlCall
	case x86asm.RET, x86asm.LRET:
		c.Type = ControlRet
		return c // No argument
	case x86asm.UD1, x86asm.UD2:
		c.Type = ControlExit
		return c // no argument
	case x86asm.JMP:
		c.Type = ControlJump
	case x86asm.JA, x86asm.JAE, x86asm.JB, x86asm.JBE, x86asm.JCXZ, x86asm.JE, x86asm.JECXZ, x86asm.JG, x86asm.JGE, x86asm.JL, x86asm.JLE, x86asm.JNE, x86asm.JNO, x86asm.JNP, x86asm.JNS, x86asm.JO, x86asm.JP, x86asm.JRCXZ, x86asm.JS,
		x86asm.LOOP, x86asm.LOOPE, x86asm.LOOPNE,
		x86asm.XBEGIN:
		c.Type = ControlJump
		c.Conditional = true
	}
	// TODO: SYSCALL, SYSENTRY, SYSEXIT, SYSRET?
	//
	// TODO: Newer table adds CALL_FAR, JMP_FAR, and RET_FAR.
	if i.Args[0] == nil || i.Args[1] != nil {
		panic(fmt.Sprintf("expected one argument, got %s", i))
	}
	if rel, ok := i.Args[0].(x86asm.Rel); ok {
		c.TargetPC = uint64(int64(i.pc) + int64(i.Inst.Len) + int64(rel))
	}
	c.Target = i.Args[0]
	return c
}

const (
	locAX = locArch
	locF0 = locAX + 16
	locM0 = locF0 + 8
	locX0 = locM0 + 8
	locES = locX0 + 16
)

var x86LocNames = [...]string{
	"AX", "CX", "DX", "BX", "SP", "BP", "SI", "DI", "R8", "R9", "R10", "R11", "R12", "R13", "R14", "R15",
	"F0", "F1", "F2", "F3", "F4", "F5", "F6", "F7",
	"M0", "M1", "M2", "M3", "M4", "M5", "M6", "M7",
	"X0", "X1", "X2", "X3", "X4", "X5", "X6", "X7", "X8", "X9", "X10", "X11", "X12", "X13", "X14", "X15",
	"ES", "CS", "SS", "DS", "FS", "GS",
}

func (e Loc) String() string {
	if e == LocMem {
		return "mem"
	}
	if idx := int(e - locAX); e >= locAX && idx < len(x86LocNames) {
		return x86LocNames[idx]
	}
	return fmt.Sprintf("Loc(%d)", e)
}

func (inst *x86Inst) Effects() (read, write LocSet) {
	// TODO: Separate each argument? Tricky with implicit effects.
	//
	// TODO: Flags effects?
	//
	// TODO: It would be nice if we could track basic frame
	// offsets, too. Though that quickly gets into alias analysis
	// territory. Could do a basic multi-level thing: "known
	// offset" -> "somewhere in frame" -> "anywhere in memory".
	// Same thing with memory: "known address" -> "anywhere in
	// memory". To do this properly we may need to look at the
	// whole instruction sequence to track changes to SP. We could
	// try to track SP precisely, or we could just say that
	// SP-relative offsets separated by an SP write have imprecise
	// aliasing. That same concept could work for any
	// register-relative memory operands. Would be nice to track
	// equality through moves, too, so if things get moved between
	// registers (and memory?) and then used as a base, we
	// understand that. (This requires some sort of fixed-point.)

	addReg := func(reg x86asm.Reg, e effect) {
		if reg == 0 {
			return
		}
		switch reg {
		case 0:
			// Unused segment/base/index.
			return
		case x86asm.IP, x86asm.EIP, x86asm.RIP:
			// We don't model the IP register, and it's
			// only used as a base for static memory
			// operands.
			if e&^r != 0 {
				panic("write of IP")
			}
			return
		}

		var rmw bool
		var idx Loc
		switch {
		case x86asm.AL <= reg && reg <= x86asm.R15B:
			// 8- and 16-bit writes modify *part* of a
			// register, making these read/write of the
			// larger register.
			rmw = true
			idx = Loc(reg-x86asm.AL) + locAX
		case x86asm.AX <= reg && reg <= x86asm.R15W:
			rmw = true
			idx = Loc(reg-x86asm.AX) + locAX
		case x86asm.EAX <= reg && reg <= x86asm.R15L:
			// These are zero-extended to 64 bits, and
			// hence *not* RMW.
			idx = Loc(reg-x86asm.EAX) + locAX
		case x86asm.RAX <= reg && reg <= x86asm.R15:
			idx = Loc(reg-x86asm.RAX) + locAX
		case x86asm.F0 <= reg && reg <= x86asm.F7:
			idx = Loc(reg-x86asm.F0) + locF0
		case x86asm.M0 <= reg && reg <= x86asm.M7:
			idx = Loc(reg-x86asm.M0) + locM0
		case x86asm.X0 <= reg && reg <= x86asm.X15:
			idx = Loc(reg-x86asm.X0) + locX0
		case x86asm.ES <= reg && reg <= x86asm.GS:
			idx = Loc(reg-x86asm.ES) + locES
		default:
			panic(fmt.Sprintf("unknown register %s in %s", reg, inst.Inst))
		}
		if rmw && e == w {
			e = rw
		}
		if e&r != 0 {
			read |= 1 << idx
		}
		if e&w != 0 {
			write |= 1 << idx
		}
	}

	// Argument effects.
	var effects []effect
	switch inst.Op {
	case x86asm.MOVHPD, x86asm.MOVHPS, x86asm.MOVLPD, x86asm.MOVLPS:
		// TODO
		panic("not implemented")
	default:
		narg := len(inst.Args)
		for i, arg := range inst.Args {
			if arg == nil {
				narg = i
				break
			}
		}
		effects = x86Args[x86ArgsKey{inst.Op, narg}]
	}
	for i, effect := range effects {
		arg := inst.Args[i]
		switch arg := arg.(type) {
		case x86asm.Reg:
			addReg(arg, effect)
		case x86asm.Mem:
			addReg(arg.Segment, r)
			addReg(arg.Base, r)
			addReg(arg.Index, r)
			if inst.Op == x86asm.LEA {
				// LEA doesn't actually access memory.
				break
			}
			if effect&r != 0 {
				read |= 1 << LocMem
			}
			if effect&w != 0 {
				write |= 1 << LocMem
			}
		case x86asm.Imm:
			if effect != r {
				panic("Imm argument has unexpected write effect")
			}
		case x86asm.Rel:
			switch inst.Op {
			case x86asm.CALL,
				x86asm.RET, x86asm.LRET,
				x86asm.JMP,
				x86asm.JA, x86asm.JAE, x86asm.JB, x86asm.JBE, x86asm.JCXZ, x86asm.JE, x86asm.JECXZ, x86asm.JG, x86asm.JGE, x86asm.JL, x86asm.JLE, x86asm.JNE, x86asm.JNO, x86asm.JNP, x86asm.JNS, x86asm.JO, x86asm.JP, x86asm.JRCXZ, x86asm.JS,
				x86asm.LOOP, x86asm.LOOPE, x86asm.LOOPNE,
				x86asm.XBEGIN:
				// Control flow target; not a memory op
			default:
				if effect&r != 0 {
					read |= 1 << LocMem
				}
				if effect&w != 0 {
					write |= 1 << LocMem
				}
			}
		}
	}

	// Implicit effects.
	implicit, ok := x86Implicit[inst.Op]
	if !ok {
		// Try with argument type lookup.
		withArg := inst.Op
		switch arg := inst.Args[0].(type) {
		case x86asm.Reg:
			switch {
			case x86asm.AL <= arg && arg <= x86asm.R15B:
				withArg |= rm8
			case x86asm.AX <= arg && arg <= x86asm.R15W:
				withArg |= rm16
			case x86asm.EAX <= arg && arg <= x86asm.R15L:
				withArg |= rm32
			case x86asm.RAX <= arg && arg <= x86asm.R15:
				withArg |= rm64
			}
		case x86asm.Mem:
			switch inst.MemBytes {
			case 1:
				withArg |= rm8
			case 2:
				withArg |= rm16
			case 4:
				withArg |= rm32
			case 8:
				withArg |= rm64
			}
		}
		implicit = x86Implicit[withArg]
	}
	for _, reg := range implicit.r {
		addReg(reg, r)
	}
	for _, reg := range implicit.w {
		addReg(reg, w)
	}

	// Break some false dependencies.
	switch inst.Op {
	case x86asm.XOR, x86asm.XORPD, x86asm.XORPS:
		if inst.Args[0] == inst.Args[1] {
			read = 0
		}
	}

	return
}
