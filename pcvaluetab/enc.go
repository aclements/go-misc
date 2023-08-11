// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// Raw file encodings of symbol table types from Go 1.21.

type rawPCHeader1 struct {
	Magic      uint32 // 0xFFFFFFF1
	Pad1, Pad2 uint8  // 0,0
	MinLC      uint8  // min instruction size
	PtrSize    uint8  // size of a ptr in bytes
}

type rawPCHeader struct {
	rawPCHeader1
	Nfunc          int     // number of functions in the module
	Nfiles         uint    // number of entries in the file tab
	TextStart      uintptr // base for function entry PC offsets in this module, equal to moduledata.text
	FuncnameOffset uintptr // offset to the funcnametab variable from pcHeader
	CuOffset       uintptr // offset to the cutab variable from pcHeader
	FiletabOffset  uintptr // offset to the filetab variable from pcHeader
	PctabOffset    uintptr // offset to the pctab variable from pcHeader
	PclnOffset     uintptr // offset to the pclntab variable from pcHeader
}

type rawFunc struct {
	EntryOff uint32 // start pc, as offset from moduledata.text/pcHeader.textStart
	NameOff  int32  // function name, as index into moduledata.funcnametab.

	Args        int32  // in/out args size
	Deferreturn uint32 // offset of start of a deferreturn call instruction from entry, if any.

	Pcsp      uint32
	Pcfile    uint32
	Pcln      uint32
	Npcdata   uint32
	CuOffset  uint32       // runtime.cutab offset of this function's CU
	StartLine int32        // line number of start of function (func keyword/TEXT directive)
	FuncID    rawABIFuncID // set for certain special runtime functions
	Flag      rawABIFuncFlag
	Pad       [1]byte // pad
	Nfuncdata uint8   // must be last, must end on a uint32-aligned boundary

	// Followed by Npcdata 4-byte offsets into pctab, then
	// Nfuncdata 4-byte offsets into moduledata.gofunc.
	// Padded to PtrSize.
}

type rawABIFuncID uint8
type rawABIFuncFlag uint8
