// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// Decode the Go 1.21 symbol table and PCDATA tables.

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"log"
	"unsafe"
)

type SymTab struct {
	dec Decoder

	header rawPCHeader

	funcNameData []byte
	pctabData    []byte
	pclnData     []byte

	Funcs  []Func
	PCTabs map[PCTabKey]*VarintPCData
}

type Func struct {
	Name    string
	EncSize int // Bytes in rawFunc
	TextLen int
	PCTabs  []PCTabKey
}

type PCTabKey uint32

type VarintPCData struct {
	Raw     []byte
	PCs     []uint32
	Vals    []int32
	TextLen uint32
}

func LoadSymTab(path string) *SymTab {
	f, err := elf.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	sec := f.Section(".gopclntab")
	if sec == nil {
		log.Fatal("missing .gopclntab")
	}
	data, err := sec.Data()
	if err != nil {
		log.Fatal("reading .gopclntab: ", err)
	}

	dec := Decoder{Order: binary.LittleEndian}
	var header rawPCHeader
	// Read the pre-header, which gives us order and size information.
	if _, err := dec.Read(data, &header.rawPCHeader1); err != nil {
		log.Fatal("reading header: ", err)
	}
	switch header.Magic {
	case 0xFFFFFFF1:
		// Go 1.20 little endian
	case 0xF1FFFFFF:
		// Go 1.20 big endian
		dec.Order = binary.BigEndian
	default:
		log.Fatalf("bad magic in header: %x", header.Magic)
	}
	dec.IntSize = int(header.PtrSize)
	dec.PtrSize = int(header.PtrSize)
	// Read the full header
	if _, err := dec.Read(data, &header); err != nil {
		log.Fatal("reading header: ", err)
	}

	// Extract data from the header.
	symtab := new(SymTab)
	symtab.dec = dec
	symtab.header = header
	symtab.funcNameData = data[header.FuncnameOffset:]
	symtab.pctabData = data[header.PctabOffset:]
	symtab.pclnData = data[header.PclnOffset:]

	// Read the func offsets table. Alternating PC, func_ offset. Ends with a
	// single "last PC".
	funcOffsets := make([]uint32, 2*header.Nfunc+1)
	if _, err := dec.Read(symtab.pclnData, funcOffsets); err != nil {
		log.Fatal("reading func offsets: ", err)
	}

	// Load functions.
	symtab.PCTabs = map[PCTabKey]*VarintPCData{}
	for i := 0; i < header.Nfunc; i++ {
		off := funcOffsets[2*i+1]

		var raw rawFunc
		rawLen, err := dec.Read(symtab.pclnData[off:], &raw)
		if err != nil {
			log.Fatal("reading func: ", err)
		}
		nameLen := bytes.IndexByte(symtab.funcNameData[raw.NameOff:], 0)
		name := string(symtab.funcNameData[raw.NameOff:][:nameLen])

		encSize := rawLen + 4*int(raw.Npcdata) + 4*int(raw.Nfuncdata)
		// Round to pointer size
		encSize = (encSize + dec.PtrSize - 1) &^ (dec.PtrSize - 1)

		// raw.Npcdata PCDATA offsets follow the header.
		pcTabs := make([]PCTabKey, 3+raw.Npcdata)
		pcTabs[0] = PCTabKey(raw.Pcfile)
		pcTabs[1] = PCTabKey(raw.Pcln)
		pcTabs[2] = PCTabKey(raw.Pcsp)
		if _, err := dec.Read(symtab.pclnData[off+uint32(rawLen):], pcTabs[3:]); err != nil {
			log.Fatal("reading pcdata offsets: ", err)
		}

		// Load PCDATA tables.
		textLen := 0
		for _, off := range pcTabs {
			if off == 0 {
				// Unused
				continue
			}
			pctab := symtab.PCTabs[off]
			if pctab == nil {
				pctab = decodeVarintPCData(symtab.pctabData[off:])
				symtab.PCTabs[off] = pctab
			}

			if textLen == 0 {
				textLen = int(pctab.TextLen)
			} else if textLen != int(pctab.TextLen) {
				log.Printf("function %s has both length %d and length %d", name, textLen, pctab.TextLen)
			}

		}

		fn := Func{Name: name, EncSize: encSize, TextLen: textLen, PCTabs: pcTabs}
		symtab.Funcs = append(symtab.Funcs, fn)
	}

	return symtab
}

// decodeVarintPCData decodes an entire varint PCDATA table.
func decodeVarintPCData(data []byte) *VarintPCData {
	tab := new(VarintPCData)
	pc, val := uint32(0), int32(-1)
	pos := 0
	for pos < len(data) && (data[pos] != 0 || pos == 0) {
		uvdelta, l := binary.Varint(data[pos:])
		if l <= 0 {
			panic("bad varint")
		}
		pos += l
		val += int32(uvdelta)

		tab.PCs = append(tab.PCs, pc)
		tab.Vals = append(tab.Vals, val)

		pcdelta, l := binary.Uvarint(data[pos:])
		if l <= 0 {
			panic("bad uvarint")
		}
		pos += l
		pc += uint32(pcdelta)
	}
	if pos == len(data) {
		log.Fatalf("PCDATA truncated")
	}
	tab.Raw = data[:pos+1]
	tab.TextLen = pc
	return tab
}

func (t *VarintPCData) Lookup(pc uint32) int32 {
	if pc > t.TextLen {
		panic("pc too big")
	}
	for i, pc1 := range t.PCs {
		if pc1 > pc {
			return t.Vals[i-1]
		}
	}
	return t.Vals[len(t.Vals)-1]
}

type pcvalueCache struct {
	entries [2][8]pcvalueCacheEnt
}

type pcvalueCacheEnt struct {
	// targetpc and off together are the key of this cache entry.
	targetpc uintptr
	off      uint32

	val   int32   // The value of this entry.
	valPC uintptr // The PC at which val starts
}

// pcvalueCacheKey returns the outermost index in a pcvalueCache to use for targetpc.
// It must be very cheap to calculate.
// For now, align to goarch.PtrSize and reduce mod the number of entries.
// In practice, this appears to be fairly randomly and evenly distributed.
func pcvalueCacheKey(targetpc uintptr) uintptr {
	return (targetpc / PtrSize) % uintptr(len(pcvalueCache{}.entries))
}

const PtrSize = 8

//go:linkname fastrandn runtime.fastrandn
func fastrandn(n uint32) uint32

func lookupVarintPCData(p []byte, targetpc uintptr, cache *pcvalueCache) (int32, uintptr) {
	// This closely follows runtime.pcdata
	//
	// TODO: Should we add the caching logic for a fairer comparison?

	// Check the cache. This speeds up walks of deep stacks, which
	// tend to have the same recursive functions over and over.
	//
	// This cache is small enough that full associativity is
	// cheaper than doing the hashing for a less associative
	// cache.
	off := uint32(uintptr(unsafe.Pointer(&p[0])))
	if cache != nil {
		x := pcvalueCacheKey(targetpc)
		for i := range cache.entries[x] {
			// We check off first because we're more
			// likely to have multiple entries with
			// different offsets for the same targetpc
			// than the other way around, so we'll usually
			// fail in the first clause.
			ent := &cache.entries[x][i]
			if ent.off == off && ent.targetpc == targetpc {
				return ent.val, ent.valPC
			}
		}
	}

	pc := uintptr(0)
	prevpc := pc
	val := int32(-1)
	for {
		var ok bool
		p, ok = step(p, &pc, &val, pc == 0)
		if !ok {
			break
		}
		if targetpc < pc {
			// Replace a random entry in the cache. Random
			// replacement prevents a performance cliff if
			// a recursive stack's cycle is slightly
			// larger than the cache.
			// Put the new element at the beginning,
			// since it is the most likely to be newly used.
			if cache != nil {
				x := pcvalueCacheKey(targetpc)
				e := &cache.entries[x]
				ci := fastrandn(uint32(len(cache.entries[x])))
				e[ci] = e[0]
				e[0] = pcvalueCacheEnt{
					targetpc: targetpc,
					off:      off,
					val:      val,
					valPC:    prevpc,
				}
			}

			return val, prevpc
		}
		prevpc = pc
	}

	panic("invalid pc-encoded table")
}

// TODO: We should read this from the header, but in the runtime it's constant,
// so for fair comparison, we make it a constant here, too.
const PCQuantum = 1

// step advances to the next pc, value pair in the encoded table.
func step(p []byte, pc *uintptr, val *int32, first bool) (newp []byte, ok bool) {
	// For both uvdelta and pcdelta, the common case (~70%)
	// is that they are a single byte. If so, avoid calling readvarint.
	uvdelta := uint32(p[0])
	if uvdelta == 0 && !first {
		return nil, false
	}
	n := uint32(1)
	if uvdelta&0x80 != 0 {
		n, uvdelta = readvarint(p)
	}
	*val += int32(-(uvdelta & 1) ^ (uvdelta >> 1))
	p = p[n:]

	pcdelta := uint32(p[0])
	n = 1
	if pcdelta&0x80 != 0 {
		n, pcdelta = readvarint(p)
	}
	p = p[n:]
	*pc += uintptr(pcdelta * PCQuantum)
	return p, true
}

// readvarint reads a varint from p.
func readvarint(p []byte) (read uint32, val uint32) {
	var v, shift, n uint32
	for {
		b := p[n]
		n++
		v |= uint32(b&0x7F) << (shift & 31)
		if b&0x80 == 0 {
			break
		}
		shift += 7
	}
	return n, v
}
